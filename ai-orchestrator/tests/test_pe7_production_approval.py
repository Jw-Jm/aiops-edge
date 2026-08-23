"""PE.7 Production Approval 流程 — TDD 测试（V9.3 Production Enablement）。

覆盖 PE.7：
- T1 完整审批链（contract signed + policy ALLOW + preview approved + credential valid）→ 允许执行
- T2 链中断（policy denied / preview 未批准 / credential 无效）→ 拒绝
- T3 Break Glass（紧急人工介入，绕过常规 approval，记录 + 事后审计）
- T4 Emergency Revoke（立即失效，阻断执行）
"""
from __future__ import annotations

import pytest

from production_approval import ApprovalChainDenied, ProductionApproval


@pytest.fixture
def approval():
    return ProductionApproval()


def _chain_all_ok():
    return {"signed": True, "policy_allows": True, "preview_approved": True, "credential_valid": True}


# ═══════════════════════════════════════════════════════
#  T1 完整审批链
# ═══════════════════════════════════════════════════════

class TestT1FullChain:
    def test_full_chain_allows(self, approval):
        assert approval.verify_chain(contract_id="c-1", **_chain_all_ok()) is True


# ═══════════════════════════════════════════════════════
#  T2 链中断
# ═══════════════════════════════════════════════════════

class TestT2ChainBroken:
    def test_policy_denied_blocks(self, approval):
        with pytest.raises(ApprovalChainDenied):
            approval.verify_chain(contract_id="c-1", signed=True, policy_allows=False, preview_approved=True, credential_valid=True)

    def test_preview_not_approved_blocks(self, approval):
        with pytest.raises(ApprovalChainDenied):
            approval.verify_chain(contract_id="c-1", signed=True, policy_allows=True, preview_approved=False, credential_valid=True)

    def test_credential_invalid_blocks(self, approval):
        with pytest.raises(ApprovalChainDenied):
            approval.verify_chain(contract_id="c-1", signed=True, policy_allows=True, preview_approved=True, credential_valid=False)


# ═══════════════════════════════════════════════════════
#  T3 Break Glass
# ═══════════════════════════════════════════════════════

class TestT3BreakGlass:
    def test_break_glass_records_audit(self, approval):
        ev = approval.break_glass(contract_id="c-1", approver="oncall-1", reason="生产中断，紧急恢复")
        assert ev["contract_id"] == "c-1"
        assert ev["approver"] == "oncall-1"
        assert ev["reason"]
        assert ev["audited"] is True  # 事后审计

    def test_break_glass_bypasses_chain(self, approval):
        # Break Glass 绕过常规 approval，但必须记录审计（事后追责）
        ev = approval.break_glass(contract_id="c-1", approver="oncall-1", reason="紧急")
        assert ev["audited"] is True


# ═══════════════════════════════════════════════════════
#  T4 Emergency Revoke
# ═══════════════════════════════════════════════════════

class TestT4EmergencyRevoke:
    def test_revoke_blocks_execution(self, approval):
        approval.emergency_revoke(contract_id="c-1")
        # 撤销后，即使链完整也拒绝执行
        with pytest.raises(ApprovalChainDenied):
            approval.verify_chain(contract_id="c-1", **_chain_all_ok())

    def test_revoke_recorded(self, approval):
        approval.emergency_revoke(contract_id="c-1")
        assert approval.is_revoked("c-1") is True
