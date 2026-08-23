"""PE.7 Production Approval 流程 — V9.3 Execution Production Enablement。

真实生产执行的完整审批链 + 应急机制（PE.7）：
- verify_chain：完整审批链（signed + policy ALLOW + preview approved + credential valid + 未 revoke）才允许执行。
- Break Glass：紧急人工介入，绕过常规 approval，但必须记录审计（事后追责）。
- Emergency Revoke：立即失效，阻断执行（即使链完整）。
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Dict, Set


class ApprovalChainDenied(ValueError):
    def __init__(self, message: str):
        self.error_code = "APPROVAL_CHAIN_DENIED"
        super().__init__(message)


class ProductionApproval:
    """内存 Production Approval 流程（MVP）。"""

    def __init__(self) -> None:
        self._revoked: Set[str] = set()
        self._audit: list = []

    def verify_chain(
        self,
        contract_id: str,
        *,
        signed: bool,
        policy_allows: bool,
        preview_approved: bool,
        credential_valid: bool,
    ) -> bool:
        """完整审批链校验。任一环节失败或已 revoke → 拒绝。"""
        if contract_id in self._revoked:
            raise ApprovalChainDenied(f"contract 已被 Emergency Revoke，阻断执行: {contract_id}")
        if not signed:
            raise ApprovalChainDenied("无 Approval Signature（signed=false）")
        if not policy_allows:
            raise ApprovalChainDenied("Policy 拒绝（policy_allows=false）")
        if not preview_approved:
            raise ApprovalChainDenied("ExecutionPreview 未批准")
        if not credential_valid:
            raise ApprovalChainDenied("credential 无效")
        return True

    def break_glass(self, *, contract_id: str, approver: str, reason: str) -> Dict[str, Any]:
        """Break Glass：紧急人工介入，绕过常规 approval，记录审计（事后追责）。"""
        ev = {
            "type": "break_glass",
            "contract_id": contract_id,
            "approver": approver,
            "reason": reason,
            "at": datetime.now(timezone.utc).isoformat(),
            "audited": True,  # 必须事后审计
        }
        self._audit.append(ev)
        return ev

    def emergency_revoke(self, contract_id: str) -> None:
        """Emergency Revoke：立即失效，阻断执行。"""
        self._revoked.add(contract_id)
        self._audit.append(
            {
                "type": "emergency_revoke",
                "contract_id": contract_id,
                "at": datetime.now(timezone.utc).isoformat(),
            }
        )

    def is_revoked(self, contract_id: str) -> bool:
        return contract_id in self._revoked

    def audit_events(self) -> list:
        return list(self._audit)
