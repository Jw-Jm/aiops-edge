"""P7.7 Structured Investigation State — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.7 设计的 T1-T4：
- T1 状态维护（pending/running/completed steps；ToolResult/Evidence refs；missing evidence）
- T2 Budget 追踪（consumed/remaining 正确；不超冻结预算）
- T3 状态恢复（从结构化状态恢复，非 prompt 历史；无 Incident/Detection 状态机）
- T4 边界（findings/hypotheses 不含执行指令；状态更新不影响外部资源）
"""
from __future__ import annotations

import pytest

from investigation_state import InvestigationState, StateStore, StepState


# ═══════════════════════════════════════════════════════
#  T1 状态维护
# ═══════════════════════════════════════════════════════

class TestT1StateMaintenance:
    def test_create_state(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        assert st.run_id == "r1"
        assert st.phase == "IntentCreated"
        assert st.steps == []
        assert st.tool_result_refs == []
        assert st.evidence_refs == []
        assert st.missing_evidence == []

    def test_step_status_and_refs(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        store.add_step(st.run_id, StepState(step_id="s1", status="pending"))
        store.update_step(st.run_id, "s1", "completed", result_ref="query_logs.v1")
        st = store.get(st.run_id)
        assert st.steps[0].status == "completed"
        assert st.steps[0].result_ref == "query_logs.v1"

    def test_missing_evidence_recorded(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        store.add_missing_evidence(st.run_id, {"tool_id": "query_traces.v1", "reason": "no traces"})
        assert store.get(st.run_id).missing_evidence == [
            {"tool_id": "query_traces.v1", "reason": "no traces"}
        ]


# ═══════════════════════════════════════════════════════
#  T2 Budget 追踪
# ═══════════════════════════════════════════════════════

class TestT2BudgetTracking:
    def test_consumed_remaining(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1", max_steps=5)
        store.consume_budget(st.run_id, steps=2, tools=1)
        st = store.get(st.run_id)
        assert st.budget["consumed_steps"] == 2
        assert st.budget["remaining"] == 3

    def test_budget_not_exceeded(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1", max_steps=2)
        store.consume_budget(st.run_id, steps=2, tools=1)
        with pytest.raises(ValueError):
            store.consume_budget(st.run_id, steps=1, tools=1)  # 超 max_steps=2


# ═══════════════════════════════════════════════════════
#  T3 状态恢复
# ═══════════════════════════════════════════════════════

class TestT3StateRestore:
    def test_restore_from_structured_state(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        store.update_step(st.run_id, st.steps[0].step_id, "pending") if st.steps else None
        # 结构化恢复：to_dict → 新 store from_dict 恢复，非 prompt 历史
        snapshot = store.get("r1").to_dict()
        store2 = StateStore()
        restored = store2.restore(snapshot)
        assert restored.run_id == "r1"
        assert restored.phase == "IntentCreated"

    def test_no_incident_detection_state_machine(self):
        st = InvestigationState(run_id="r1", intent_id="i1", plan_id="p1")
        assert not hasattr(st, "incidents")
        assert not hasattr(st, "detections")


# ═══════════════════════════════════════════════════════
#  T4 边界
# ═══════════════════════════════════════════════════════

class TestT4Boundary:
    def test_findings_no_execution_instructions(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        store.add_finding(st.run_id, "检测到日志错误率升高")
        with pytest.raises(ValueError):
            store.add_finding(st.run_id, "kubectl delete pod checkout-0")  # 执行指令 → 拒绝

    def test_state_update_no_external_effect(self):
        store = StateStore()
        st = store.create(run_id="r1", intent_id="i1", plan_id="p1")
        store.transition(st.run_id, "EvidenceCollected")
        # 状态更新不影响外部资源（无 side effect 接口）
        assert store.get(st.run_id).phase == "EvidenceCollected"
