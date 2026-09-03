"""P8.1 Execution Contract — V9.3 Phase8 一次性执行许可证（Approval ≠ 授权执行）。

核心原则（P8.1 v0.2）：
- Approval 不是授权执行，而是生成一次性执行许可证。
- contract_hash 不可变（SHA256(contract_id + actions + resources + expire_time)），执行时校验防篡改。
- execution_lock 状态机：active → acquire_lock → executing → executed；同 contract 二次 acquire_lock 拒绝（防并发重复执行）。
- 状态机单向：draft → approved → active → executing → (expired|revoked|executed)；revoked 不可逆。
- allowed_actions 白名单（缺省拒绝）；max_scope 不自动扩大。
- Contract 不持 Secret（credential 委托在 P8.2/P8.3）。
"""
from __future__ import annotations

import hashlib
import json
import uuid
from dataclasses import dataclass, field, replace
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

CONTRACT_STATUSES = {"draft", "approved", "active", "executing", "executed", "expired", "revoked"}
CREATED_VERSION = "v1"

_VALID_TRANSITIONS = {
    "draft": {"approved", "revoked"},
    "approved": {"active", "revoked"},
    "active": {"executing", "expired", "revoked"},
    "executing": {"executed", "revoked"},
    "executed": set(),
    "expired": set(),
    "revoked": set(),
}


class ContractNotExecutable(ValueError):
    def __init__(self, message: str):
        self.error_code = "CONTRACT_NOT_EXECUTABLE"
        super().__init__(message)


def _sha256(*parts) -> str:
    h = hashlib.sha256()
    for p in parts:
        h.update(str(p).encode("utf-8"))
    return h.hexdigest()


def _contract_hash(contract_id, actions, resources, expire_time,
                   tools=None, max_scope="", rollback_policy=None) -> str:
    # S7：hash 必须覆盖全部授权字段。漏掉 tools/max_scope/rollback_policy 时，
    # 篡改这些字段（扩大工具面 / resource→cluster 提权 / 改回滚策略）不会改变 hash。
    return _sha256(
        contract_id,
        ",".join(sorted(actions)),
        ",".join(sorted(resources)),
        ",".join(sorted(tools or [])),
        str(max_scope),
        json.dumps(rollback_policy or {}, sort_keys=True, separators=(",", ":"), default=str),
        str(expire_time),
    )


@dataclass
class ExecutionContract:
    contract_id: str
    plan_id: str
    intent_id: str
    run_id: str
    requested_by: str
    allowed_tools: List[str]
    allowed_resources: List[str]
    allowed_actions: List[str]
    max_scope: str
    expire_time: datetime
    rollback_policy: Dict[str, Any]
    contract_hash: str = ""
    signature: str = ""
    created_version: str = CREATED_VERSION
    approved_by: str = ""
    approved_at: Optional[datetime] = None
    executed_by: str = ""
    status: str = "draft"
    created_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))

    def __post_init__(self) -> None:
        if self.status not in CONTRACT_STATUSES:
            raise ValueError(f"非法 status: {self.status}")
        if self.max_scope not in {"cluster", "namespace", "resource"}:
            raise ValueError(f"非法 max_scope: {self.max_scope}")


class ExecutionContractStore:
    """内存 ExecutionContract Store（MVP）。真实持久化属后续阶段。"""

    def __init__(self) -> None:
        self._store: Dict[str, ExecutionContract] = {}

    def create(
        self,
        *,
        plan_id: str,
        intent_id: str,
        run_id: str,
        requested_by: str,
        allowed_tools: List[str],
        allowed_resources: List[str],
        allowed_actions: List[str],
        max_scope: str,
        expire_time: datetime,
        rollback_policy: Dict[str, Any],
    ) -> ExecutionContract:
        contract_id = str(uuid.uuid4())
        ch = _contract_hash(contract_id, allowed_actions, allowed_resources, expire_time,
                            tools=allowed_tools, max_scope=max_scope, rollback_policy=rollback_policy)
        c = ExecutionContract(
            contract_id=contract_id,
            plan_id=plan_id,
            intent_id=intent_id,
            run_id=run_id,
            requested_by=requested_by,
            allowed_tools=list(allowed_tools),
            allowed_resources=list(allowed_resources),
            allowed_actions=list(allowed_actions),
            max_scope=max_scope,
            expire_time=expire_time,
            rollback_policy=dict(rollback_policy),
            contract_hash=ch,
            status="draft",
        )
        self._store[contract_id] = c
        return c

    def approve(self, contract_id: str, *, approved_by: str) -> ExecutionContract:
        c = self._transition(contract_id, "approved")
        signature = _sha256(c.contract_hash, approved_by, c.created_version)
        updated = replace(c, approved_by=approved_by, approved_at=datetime.now(timezone.utc), signature=signature)
        self._store[contract_id] = updated
        return updated

    def activate(self, contract_id: str) -> ExecutionContract:
        c = self._store.get(contract_id)
        if c is None:
            raise ContractNotExecutable(f"contract 不存在: {contract_id}")
        if c.status != "approved":
            raise ContractNotExecutable(f"只有 approved 可 activate，当前 {c.status}")
        if _now() > c.expire_time:
            updated = replace(c, status="expired")
            self._store[contract_id] = updated
            raise ContractNotExecutable("contract 已过期，不可 activate")
        return self._transition(contract_id, "active")

    def acquire_lock(self, contract_id: str) -> ExecutionContract:
        c = self._store.get(contract_id)
        if c is None:
            raise ContractNotExecutable(f"contract 不存在: {contract_id}")
        if c.status != "active":
            raise ContractNotExecutable(f"只有 active 可 acquire_lock，当前 {c.status}")
        return self._transition(contract_id, "executing")

    def complete(self, contract_id: str) -> ExecutionContract:
        return self._transition(contract_id, "executed")

    def revoke(self, contract_id: str) -> ExecutionContract:
        c = self._store.get(contract_id)
        if c is None:
            raise ContractNotExecutable(f"contract 不存在: {contract_id}")
        if c.status in {"executed", "expired", "revoked"}:
            raise ContractNotExecutable(f"{c.status} 为终态，不可 revoke")
        return self._transition(contract_id, "revoked")

    def verify_hash(self, contract_id: str) -> bool:
        c = self._store.get(contract_id)
        if c is None:
            return False
        expected = _contract_hash(c.contract_id, c.allowed_actions, c.allowed_resources, c.expire_time,
                                  tools=c.allowed_tools, max_scope=c.max_scope, rollback_policy=c.rollback_policy)
        return expected == c.contract_hash

    def is_executable(self, contract_id: str) -> bool:
        c = self._store.get(contract_id)
        if c is None:
            return False
        return c.status == "active" and _now() <= c.expire_time

    def get(self, contract_id: str) -> Optional[ExecutionContract]:
        return self._store.get(contract_id)

    def _transition(self, contract_id: str, new_status: str) -> ExecutionContract:
        if new_status not in CONTRACT_STATUSES:
            raise ValueError(f"非法 status: {new_status}")
        c = self._store.get(contract_id)
        if c is None:
            raise ContractNotExecutable(f"contract 不存在: {contract_id}")
        allowed = _VALID_TRANSITIONS.get(c.status, set())
        if new_status not in allowed:
            raise ContractNotExecutable(f"非法迁移: {c.status} → {new_status}")
        updated = replace(c, status=new_status)
        self._store[contract_id] = updated
        return updated


def _now():
    return datetime.now(timezone.utc)
