"""P7.6 Planner — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.6 设计的 T1-T5：
- T1 Plan Proposal（steps + depends_on；只产提案无最终根因；requires_human_approval=true 强制）
- T2 Budget（max_steps/max_tools 严格执行；超限 → budget_exceeded；parallel 共享预算）
- T3 Dependency & Parallel（依赖 step 按序；互不依赖可并行；依赖缺失 → failed）
- T4 MissingEvidence（证据不足 → slot；不强行归因；受 budget 约束）
- T5 Approval Boundary（Planner 不直连 Executor；awaiting_approval 停止）
"""
from __future__ import annotations

import pytest

from intent_engine import IntentEngine
from planner import Planner
from tool_registry import ToolRegistry, init_default_tool_registry


def _reset_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    init_default_tool_registry()


@pytest.fixture(autouse=True)
def _fresh():
    _reset_registry()
    yield
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()


TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"


@pytest.fixture
def planner():
    return Planner(max_steps=5, max_tools=10, max_latency_ms=60000)


@pytest.fixture
def intent():
    eng = IntentEngine()
    return eng.create_intent(
        intent="调查 checkout 日志错误",
        action_mode="read_only",
        target_type="service",
        target_resource_id="checkout",
        tenant_id=TENANT,
        primary_cluster_id=CLUSTER,
        capability="observability.logs.read",
        source="user_explicit",
        time_range_start="2026-08-20T00:00:00Z",
        time_range_end="2026-08-20T01:00:00Z",
    )


def _steps():
    return [
        {"step_id": "s1", "tool_id": "query_logs.v1", "params": {"service": "checkout"}, "depends_on": []},
        {"step_id": "s2", "tool_id": "query_metrics.v1", "params": {"service": "checkout"}, "depends_on": ["s1"]},
    ]


# ═══════════════════════════════════════════════════════
#  T1 Plan Proposal
# ═══════════════════════════════════════════════════════

class TestT1PlanProposal:
    def test_propose_plan_creates_steps(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        assert plan.intent_id == intent.intent_id
        assert len(plan.steps) == 2
        assert plan.steps[1].depends_on == ["s1"]
        assert plan.steps[0].tool_id == "query_logs.v1"

    def test_no_root_cause_output(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        # 只产提案，无最终根因
        assert plan.result.tool_results == []
        assert plan.result.evidence == []
        assert not hasattr(plan, "root_cause")

    def test_requires_human_approval_forced(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        assert plan.requires_human_approval is True


# ═══════════════════════════════════════════════════════
#  T2 Budget
# ═══════════════════════════════════════════════════════

class TestT2Budget:
    def test_max_steps_exceeded(self, intent):
        p = Planner(max_steps=2, max_tools=10)
        plan = p.propose_plan(intent, _steps())  # 2 steps == max_steps（不超）
        assert plan.status == "pending"
        p2 = Planner(max_steps=1, max_tools=10)
        plan2 = p2.propose_plan(intent, _steps())  # 2 steps > max_steps=1
        assert plan2.status == "budget_exceeded"

    def test_max_tools_exceeded(self, intent):
        steps = [
            {"step_id": "a", "tool_id": "query_logs.v1", "params": {}, "depends_on": []},
            {"step_id": "b", "tool_id": "query_metrics.v1", "params": {}, "depends_on": []},
            {"step_id": "c", "tool_id": "query_traces.v1", "params": {}, "depends_on": []},
        ]
        p = Planner(max_steps=10, max_tools=2)
        plan = p.propose_plan(intent, steps)  # 3 个不同 tool > max_tools=2
        assert plan.status == "budget_exceeded"

    def test_parallel_shared_budget(self, planner, intent):
        # parallel 分支共享 max_steps 预算
        steps = [
            {"step_id": "a", "tool_id": "query_logs.v1", "params": {}, "depends_on": []},
            {"step_id": "b", "tool_id": "query_metrics.v1", "params": {}, "depends_on": []},
        ]
        p = Planner(max_steps=2, max_tools=10)
        plan = p.propose_plan(intent, steps)
        assert plan.status == "pending"
        assert plan.budget.consumed_steps == 2


# ═══════════════════════════════════════════════════════
#  T3 Dependency & Parallel
# ═══════════════════════════════════════════════════════

class TestT3DependencyParallel:
    def test_dependency_order(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        # s1 无依赖 → ready；s2 依赖 s1 → 未 ready
        ready = planner.ready_steps(plan.plan_id)
        assert "s1" in ready
        assert "s2" not in ready

    def test_dependency_satisfied_then_ready(self, planner, intent):

        plan = planner.propose_plan(intent, _steps())
        from internal_query_client import QueryResult
        from tool_result import normalize_tool_result

        tr = normalize_tool_result(
            outcome=QueryResult(200, {"logs": [{"ts": 1}]}),
            tool=ToolRegistry.get("query_logs.v1"),
            tenant_id=TENANT, cluster_id=CLUSTER, request_id="r", query_id="q",
            time_range="t", source_system="query-api",
            started_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
            finished_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
        )
        planner.complete_step(plan.plan_id, "s1", tr)
        ready = planner.ready_steps(plan.plan_id)
        assert "s2" in ready

    def test_missing_dependency_failed(self, planner, intent):
        steps = [
            {"step_id": "s1", "tool_id": "query_logs.v1", "params": {}, "depends_on": ["nonexistent"]},
        ]
        plan = planner.propose_plan(intent, steps)
        assert plan.steps[0].status == "failed"  # 依赖缺失 → failed

    def test_cycle_rejected(self, planner, intent):
        steps = [
            {"step_id": "a", "tool_id": "query_logs.v1", "params": {}, "depends_on": ["b"]},
            {"step_id": "b", "tool_id": "query_metrics.v1", "params": {}, "depends_on": ["a"]},
        ]
        with pytest.raises(ValueError):
            planner.propose_plan(intent, steps)


# ═══════════════════════════════════════════════════════
#  T4 MissingEvidence
# ═══════════════════════════════════════════════════════

class TestT4MissingEvidence:
    def test_add_missing_evidence_slot(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        planner.add_missing_evidence(plan.plan_id, "query_traces.v1", "observability.traces.read", "insufficient data")
        assert len(plan.result.missing_evidence) == 1
        me = plan.result.missing_evidence[0]
        assert me["tool_id"] == "query_traces.v1"
        assert me["reason"] == "insufficient data"

    def test_no_forced_attribution(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        planner.add_missing_evidence(plan.plan_id, "query_traces.v1", "observability.traces.read", "no traces")
        # 不强行归因根因：missing_evidence 是 slot，不是根因结论
        assert plan.result.missing_evidence
        assert not hasattr(plan, "root_cause")


# ═══════════════════════════════════════════════════════
#  T5 Approval Boundary
# ═══════════════════════════════════════════════════════

class TestT5ApprovalBoundary:
    def test_no_executor_direct_connection(self, planner, intent):
        # Planner 不持有 executor / 不触发执行
        assert not hasattr(planner, "executor")
        assert not hasattr(planner, "execute")

    def test_finalize_awaits_approval(self, planner, intent):
        plan = planner.propose_plan(intent, _steps())
        plan = planner.finalize(plan.plan_id)
        assert plan.status == "awaiting_approval"
        assert plan.requires_human_approval is True
