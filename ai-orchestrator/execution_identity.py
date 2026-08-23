"""P8.2 Execution Identity — V9.3 Phase8 第四层执行身份（谁批准/谁发起/谁执行）。

核心原则（P8.2 v0.2）：
- 审计链三问：Who approved?（approved_by）Who requested?（requested_by）Who executed?（executed_by）
- executed_by 是 Execution Identity（独立实体，UUID），≠ approved_by(Human) ≠ requested_by(Agent)。
- executed_by 由系统从 contract 派生，Agent 不能自选（scope 超 contract → 拒绝）。
- 一次性：随 contract expire_time 过期；revoked 阻断。
- credential_ref 只引用委托凭据（经 Broker），不存 Secret。
- 身份声明 ≠ 执行权限（F2 延续）。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, Optional

from execution_contract import ExecutionContract, ExecutionContractStore

IDENTITY_STATUSES = {"issued", "active", "expired", "revoked"}
_IDENTITY_TYPES = {"user", "service", "execution"}


class IdentityNotActive(ValueError):
    def __init__(self, message: str):
        self.error_code = "IDENTITY_NOT_ACTIVE"
        super().__init__(message)


class IdentityNotAuthorized(ValueError):
    def __init__(self, message: str):
        self.error_code = "IDENTITY_NOT_AUTHORIZED"
        super().__init__(message)


@dataclass
class ExecutionIdentity:
    identity_id: str
    run_id: str
    contract_id: str
    executed_by: str
    identity_type: str
    principal_id: str
    credential_ref: str
    scope: str
    issued_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    expire_time: Optional[datetime] = None
    status: str = "issued"

    def __post_init__(self) -> None:
        if self.status not in IDENTITY_STATUSES:
            raise ValueError(f"非法 status: {self.status}")
        if self.identity_type not in _IDENTITY_TYPES:
            raise ValueError(f"非法 identity_type: {self.identity_type}")


class ExecutionIdentityStore:
    """内存 Execution Identity Store（MVP）。"""

    def __init__(self, contract_store: Optional[ExecutionContractStore] = None) -> None:
        self._contract_store = contract_store or ExecutionContractStore()
        self._store: Dict[str, ExecutionIdentity] = {}

    def issue(
        self,
        *,
        contract_id: str,
        run_id: str,
        executed_by: str,
        identity_type: str,
        principal_id: str,
        credential_ref: str,
        scope: str,
        expire_time: Optional[datetime] = None,
    ) -> ExecutionIdentity:
        """签发一次性执行身份。executed_by 由系统派生，Agent 不能自选超出 scope。"""
        contract = self._contract_store.get(contract_id)
        if contract is None:
            raise IdentityNotAuthorized(f"contract 不存在: {contract_id}")
        # scope 收敛：executed_by 的 scope 必须 ∈ contract.allowed_resources（Agent 不能自选扩大）
        if scope not in contract.allowed_resources:
            raise IdentityNotAuthorized(f"scope 超出 contract.allowed_resources: {scope}")
        if expire_time is None:
            expire_time = contract.expire_time
        identity = ExecutionIdentity(
            identity_id=str(uuid.uuid4()),
            run_id=run_id,
            contract_id=contract_id,
            executed_by=executed_by,
            identity_type=identity_type,
            principal_id=principal_id,
            credential_ref=credential_ref,
            scope=scope,
            expire_time=expire_time,
            status="issued",
        )
        self._store[identity.identity_id] = identity
        return identity

    def activate(self, identity_id: str) -> ExecutionIdentity:
        identity = self._store.get(identity_id)
        if identity is None:
            raise IdentityNotActive(f"identity 不存在: {identity_id}")
        if _now() > identity.expire_time:
            raise IdentityNotActive("execution identity 已过期")
        identity.status = "active"
        return identity

    def revoke(self, identity_id: str) -> ExecutionIdentity:
        identity = self._store.get(identity_id)
        if identity is None:
            raise IdentityNotActive(f"identity 不存在: {identity_id}")
        identity.status = "revoked"
        return identity

    def is_active(self, identity_id: str) -> bool:
        identity = self._store.get(identity_id)
        if identity is None:
            return False
        if identity.status != "active":
            return False
        if identity.expire_time and _now() > identity.expire_time:
            return False
        return True

    def audit_trace(self, identity_id: str, contract: ExecutionContract) -> Dict[str, Any]:
        """完整审计链：谁批准/谁发起/谁执行。缺任一 → 拒绝。"""
        identity = self._store.get(identity_id)
        if identity is None:
            raise IdentityNotAuthorized(f"identity 不存在，审计链断裂: {identity_id}")
        if not contract.approved_by or not contract.requested_by or not identity.executed_by:
            raise IdentityNotAuthorized("审计链缺失（approved_by/requested_by/executed_by）")
        return {
            "approved_by": contract.approved_by,
            "requested_by": contract.requested_by,
            "executed_by": identity.executed_by,
            "run_id": identity.run_id,
            "contract_id": identity.contract_id,
        }

    def get(self, identity_id: str) -> Optional[ExecutionIdentity]:
        return self._store.get(identity_id)


def _now():
    return datetime.now(timezone.utc)
