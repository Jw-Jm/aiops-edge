"""EX.3 Execution Adapter Boundary — V9.3 Execution Infrastructure Adapter Interface v1。

基于 P8.3 Adapter Boundary，增强 Execution Infrastructure 能力（不连真实系统）：
- EX.1 Approval Signature 集成：执行前验签（signer==approved_by）；需签名时无签名拒绝。
- EX.2 Credential Broker 集成：credential 经 Broker 获取（不直接持 credential）。
- R4.1 Execution Action Idempotency：同 idempotency_key 二次 → 返回已执行结果（防 timeout retry 重复执行）。
- R4.2 Adapter Permission Snapshot：执行时保存 contract_permission_snapshot（RBAC 变化时审计当时依据）。
- before_state/after_state/target_snapshot 审计字段（可靠 RCA）。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Callable, Dict, List, Optional

from approval_signature import ApprovalSignature
from credential_broker import CredentialBroker
from execution_contract import ExecutionContract
from execution_identity import ExecutionIdentity

ADAPTER_STATUSES = {"success", "failed", "denied", "dry_run"}


@dataclass
class AdapterRequest:
    contract_id: str
    credential_ref: str
    target: Dict[str, Any]
    action: str
    params: Dict[str, Any] = field(default_factory=dict)
    dry_run: bool = False
    idempotency_key: str = ""  # R4.1


@dataclass
class AdapterResult:
    status: str
    output: Dict[str, Any] = field(default_factory=dict)
    rollback_ref: str = ""
    executed_at: Optional[datetime] = None
    adapter_id: str = ""
    target_snapshot: Optional[Dict[str, Any]] = None
    before_state: Optional[Dict[str, Any]] = None
    after_state: Optional[Dict[str, Any]] = None
    execution_trace_id: str = ""
    reason: str = ""
    contract_permission_snapshot: Optional[Dict[str, Any]] = None  # R4.2
    credential_id: str = ""  # EX.2 broker 获取

    def __post_init__(self) -> None:
        if self.status not in ADAPTER_STATUSES:
            raise ValueError(f"非法 status: {self.status}")


class ExecutionAdapter:
    """内存 MVP Adapter（不连真实系统）。增强 Execution Infrastructure 安全能力。"""

    def __init__(self, *, adapter_id: str, broker: Optional[CredentialBroker] = None, real_adapter: Optional["K8sAdapter"] = None) -> None:
        self.adapter_id = adapter_id
        self._broker = broker
        self._real_adapter = real_adapter
        self._executed: Dict[str, AdapterResult] = {}  # R4.1 idempotency 缓存
        self._require_signature = False  # EX.1
        self._approval_verifier: Optional[Callable] = None
        self._approval_public_key = None

    def verify_contract_scope(self, request: AdapterRequest, contract: ExecutionContract) -> bool:
        """二次校验 action/target scope（Defense in Depth，fail-closed）。"""
        if contract.status != "active":
            return False
        if _now() > contract.expire_time:
            return False
        if request.action not in contract.allowed_actions:
            return False
        namespace = (request.target or {}).get("namespace", "")
        if namespace and namespace not in contract.allowed_resources:
            return False
        return True

    def execute(
        self,
        request: AdapterRequest,
        contract: ExecutionContract,
        approval_signature: Optional[ApprovalSignature] = None,
        execution_identity: Optional[ExecutionIdentity] = None,
    ) -> AdapterResult:
        """执行一次动作（内存模拟）。fail-closed：denied 不抛。"""
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败（contract 非 active / 白名单外 / 资源范围外）")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        # R4.1 idempotency：同 idempotency_key → 返回已执行结果（防重复执行）
        if request.idempotency_key and request.idempotency_key in self._executed:
            return self._executed[request.idempotency_key]
        # EX.1 Approval Signature：需签名时无签名/验签失败 → denied
        if self._require_signature or approval_signature is not None:
            if approval_signature is None or not self._verify_signature(approval_signature, contract):
                return self._denied("Approval Signature 缺失或验签失败")
        # 真实模式：scope/签名/幂等前置校验通过后委托真实 K8s 适配器（仍二次校验 scope）
        if self._real_adapter is not None:
            if request.dry_run:
                return self._real_adapter.dry_run(request, contract)
            return self._real_adapter.execute(request, contract)
        # EX.2 Credential Broker：经 Broker 获取 short-lived 凭据
        credential_id = ""
        if self._broker is not None and execution_identity is not None:
            cred = self._broker.delegate(
                contract=contract, execution_identity=execution_identity, adapter_id=self.adapter_id
            )
            credential_id = cred.credential_id
        if request.dry_run:
            return self._denied("dry_run 应走 dry_run()", status="dry_run")

        trace_id = str(uuid.uuid4())
        before_state = {"namespace": request.target.get("namespace"), "resource_id": request.target.get("resource_id"), "replicas": 3}
        after_state = {**before_state, "replicas": 5, "action": request.action}
        # R4.2 permission snapshot：记录执行时的权限依据（RBAC 变化审计）
        permission_snapshot = {
            "contract_id": contract.contract_id,
            "allowed_actions": list(contract.allowed_actions),
            "allowed_resources": list(contract.allowed_resources),
            "max_scope": contract.max_scope,
        }
        result = AdapterResult(
            status="success",
            output={"action": request.action, "target": request.target},
            rollback_ref=f"rollback::{trace_id}",
            executed_at=datetime.now(timezone.utc),
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            before_state=before_state,
            after_state=after_state,
            execution_trace_id=trace_id,
            contract_permission_snapshot=permission_snapshot,
            credential_id=credential_id,
        )
        if request.idempotency_key:
            self._executed[request.idempotency_key] = result
        return result

    def dry_run(self, request: AdapterRequest, contract: ExecutionContract) -> AdapterResult:
        """无副作用预览。"""
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        return AdapterResult(
            status="dry_run",
            output={"action": request.action, "preview": True},
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            execution_trace_id=str(uuid.uuid4()),
        )

    def _verify_signature(self, sig: ApprovalSignature, contract: ExecutionContract) -> bool:
        if self._approval_verifier is None or self._approval_public_key is None:
            return False  # 未配置验签器 → fail-closed
        contract_fields = {
            "contract_id": contract.contract_id,
            "actions": contract.allowed_actions,
            "resources": contract.allowed_resources,
            "expire_time": str(contract.expire_time),
        }
        try:
            return bool(
                self._approval_verifier(sig, contract_fields, self._approval_public_key, contract.approved_by)
            )
        except Exception:
            return False

    def _denied(self, reason: str, status: str = "denied") -> AdapterResult:
        return AdapterResult(
            status=status,
            reason=reason,
            adapter_id=self.adapter_id,
            execution_trace_id=str(uuid.uuid4()),
        )


def _now():
    return datetime.now(timezone.utc)
