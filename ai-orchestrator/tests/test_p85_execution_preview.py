"""P8.5 ExecutionPreview / Simulation — TDD 测试（V9.3 Phase8，内存 MVP）。

覆盖 P8.5 设计 v0.2 的 T1-T6：
- T1 生成 preview 无副作用
- T2 impact/risk/rollback 显式评估
- T3 preview 未确认 → 不真实执行
- T4 approved → 允许 adapter 执行
- T5 preview 绑定 contract（无 contract 不生成）
- T6 状态快照（缺 environment_snapshot/expected_change → 拒绝 approved；Agent 不能自动 rollback）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from execution_preview import ExecutionPreviewStore, PreviewNotApproved, PreviewRejected


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    return store.create(
        plan_id="p", intent_id="i", run_id="r", requested_by="a",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )


@pytest.fixture
def preview_store():
    return ExecutionPreviewStore()


def _gen(preview_store, contract, **over):
    kw = dict(
        contract=contract,
        target={"namespace": "ns-a", "resource_type": "pod", "resource_id": "nginx"},
        impact="1 pod restart",
        risk="medium",
        actions=["restart"],
        environment_snapshot={"namespace": "ns-a", "deployment": "nginx"},
        resource_version="pod version=v1, restartCount=3",
        expected_change={"restartCount": "3→4"},
        rollback_plan={"available": True, "note": "restart 无状态"},
    )
    kw.update(over)
    return preview_store.generate(**kw)


# ═══════════════════════════════════════════════════════
#  T1 无副作用
# ═══════════════════════════════════════════════════════

class TestT1NoSideEffect:
    def test_generate_no_side_effect(self, preview_store, contract):
        p = _gen(preview_store, contract)
        assert p.status == "pending"
        assert p.impact == "1 pod restart"


# ═══════════════════════════════════════════════════════
#  T2 显式评估
# ═══════════════════════════════════════════════════════

class TestT2ExplicitEvaluation:
    def test_impact_risk_rollback(self, preview_store, contract):
        p = _gen(preview_store, contract, impact="2 pods", risk="high", actions=["restart", "scale"])
        assert p.impact == "2 pods"
        assert p.risk == "high"
        assert p.actions == ["restart", "scale"]
        assert p.rollback_plan["available"] is True


# ═══════════════════════════════════════════════════════
#  T3 未确认不执行
# ═══════════════════════════════════════════════════════

class TestT3NotConfirmed:
    def test_pending_not_approved(self, preview_store, contract):
        p = _gen(preview_store, contract)
        assert preview_store.is_approved(p.preview_id) is False


# ═══════════════════════════════════════════════════════
#  T4 approved 允许执行
# ═══════════════════════════════════════════════════════

class TestT4Approved:
    def test_approve_allows(self, preview_store, contract):
        p = _gen(preview_store, contract)
        p = preview_store.approve(p.preview_id)
        assert p.status == "approved"
        assert preview_store.is_approved(p.preview_id) is True


# ═══════════════════════════════════════════════════════
#  T5 绑定 contract
# ═══════════════════════════════════════════════════════

class TestT5BoundToContract:
    def test_missing_contract_rejected(self, preview_store):
        with pytest.raises(ValueError):
            _gen(preview_store, None)


# ═══════════════════════════════════════════════════════
#  T6 状态快照 + Rollback 边界
# ═══════════════════════════════════════════════════════

class TestT6SnapshotAndRollback:
    def test_missing_snapshot_rejected(self, preview_store, contract):
        p = _gen(preview_store, contract, environment_snapshot=None, expected_change=None)
        with pytest.raises(PreviewRejected):
            preview_store.approve(p.preview_id)  # 缺状态快照 → 拒绝 approved

    def test_agent_cannot_auto_rollback(self, preview_store, contract):
        # rollback 需 Human 批准 + contract；preview 不自动触发 rollback
        p = _gen(preview_store, contract)
        assert p.rollback_plan["available"] is True
        # 无自动 rollback 触发接口
        assert not hasattr(preview_store, "auto_rollback")
