"""EX.2 Credential Broker — V9.3 Execution Infrastructure 凭据委托。

核心原则（EX.2 + R4.3）：
- credential_ref 只存引用；Broker 是唯一接触真实凭据的组件。
- Agent/Planner/Evidence 永不接触凭据内容（Evidence 不存 Secret）。
- 最小权限：credential scope 只含 contract.allowed_resources/allowed_actions（非全 cluster admin）。
- short-lived：随 contract expire_time 过期；revoke 撤销。
- R4.3 审计：记录 credential_issue_event（who/when/contract/adapter/scope/expire）——
  回答"哪个执行拿过哪个 credential"。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from execution_contract import ExecutionContract, ExecutionContractStore
from execution_identity import ExecutionIdentity


@dataclass
class ShortLivedCredential:
    credential_id: str
    credential_ref: str
    audience: str
    scope: Dict[str, Any]
    issued_for_adapter: str
    expire_time: Optional[datetime] = None
    revoked: bool = False


class CredentialBroker:
    """内存 Credential Broker（MVP）。真实凭据后端属后续。"""

    def __init__(self, contract_store: Optional[ExecutionContractStore] = None) -> None:
        self._contract_store = contract_store or ExecutionContractStore()
        self._store: Dict[str, ShortLivedCredential] = {}
        self._audit: List[Dict[str, Any]] = []

    def delegate(
        self,
        *,
        contract: ExecutionContract,
        execution_identity: ExecutionIdentity,
        adapter_id: str,
    ) -> ShortLivedCredential:
        """签发最小权限 short-lived 凭据，并记录审计事件（R4.3）。"""
        # 最小权限 scope（从 contract 白名单，非全 cluster admin）
        scope = {
            "cluster": False,  # 非全集群 admin
            "namespace": contract.allowed_resources[0] if contract.allowed_resources else "",
            "actions": list(contract.allowed_actions),
        }
        credential = ShortLivedCredential(
            credential_id=str(uuid.uuid4()),
            credential_ref=execution_identity.credential_ref,
            audience=adapter_id,
            scope=scope,
            issued_for_adapter=adapter_id,
            expire_time=contract.expire_time,
        )
        self._store[credential.credential_id] = credential
        # R4.3 审计：回答"哪个执行拿过哪个 credential"
        self._audit.append(
            {
                "credential_id": credential.credential_id,
                "contract_id": contract.contract_id,
                "adapter_id": adapter_id,
                "who": execution_identity.executed_by,
                "scope": {"namespace": scope["namespace"], "actions": scope["actions"]},
                "expire": credential.expire_time,
                "issued_at": datetime.now(timezone.utc).isoformat(),
            }
        )
        return credential

    def revoke(self, credential_id: str) -> None:
        cred = self._store.get(credential_id)
        if cred is not None:
            cred.revoked = True

    def is_valid(self, credential_id: str) -> bool:
        cred = self._store.get(credential_id)
        if cred is None:
            return False
        if cred.revoked:
            return False
        if cred.expire_time and _now() > cred.expire_time:
            return False
        return True

    def verify_production_constraints(self, contract: ExecutionContract, credential: ShortLivedCredential) -> bool:
        """生产准入校验（PE.3）：scope ⊆ contract 白名单、TTL ≤ contract expire、未 revoked。"""
        if credential.revoked:
            return False
        if credential.scope["namespace"] not in contract.allowed_resources:
            return False
        if not set(credential.scope["actions"]).issubset(set(contract.allowed_actions)):
            return False
        if credential.expire_time and contract.expire_time and credential.expire_time > contract.expire_time:
            return False  # TTL 超限
        return True

    def audit_events(self) -> List[Dict[str, Any]]:
        return list(self._audit)

    def get(self, credential_id: str) -> Optional[ShortLivedCredential]:
        return self._store.get(credential_id)


def _now():
    return datetime.now(timezone.utc)
