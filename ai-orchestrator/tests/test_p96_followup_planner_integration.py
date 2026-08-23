"""P9.6 Follow-up Planner — 并入现有 Planner（评审加固测试）。

评审修复：follow-up 是现有 Planner 的追加步骤能力，共享原 Plan DAG、预算、
轮数和 Registry 校验。不开启第二套 Planner / 第二调查图。
"""
import pytest

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


def _intent():
    from intent_engine import IntentEngine

    eng = IntentEngine()
    return eng.create_intent(
        intent="调查 checkout 错误", action_mode="read_only", target_type="service",
        target_resource_id="checkout", tenant_id=TENANT, primary_cluster_id=CLUSTER,
        capability="observability.logs.read", source="user_explicit",
        time_range_start="2026-08-20T00:00:00Z", time_range_end="2026-08-20T01:00:00Z",
    )


def _planner(max_steps=10, max_tools=20):
    from planner import Planner

    return Planner(max_steps=max_steps, max_tools=max_tools, max_latency_ms=60000)


def _steps():
    return [
        {"step_id": "s1", "tool_id": "query_logs.v1", "params": {"service": "checkout"}, "depends_on": []},
    ]


def test_followup_adds_step_to_existing_plan():
    p = _planner()
    plan = p.propose_plan(_intent(), _steps())
    before = len(plan.steps)
    p.add_followup_step(plan.plan_id, "query_traces.v1", "observability.traces.read", depends_on=["s1"])
    assert len(plan.steps) == before + 1
    added = plan.steps[-1]
    assert added.tool_id == "query_traces.v1"
    assert added.depends_on == ["s1"]


def test_followup_validates_registered_tool():
    from planner import FollowUpValidationError

    p = _planner()
    plan = p.propose_plan(_intent(), _steps())
    # 未注册 tool → 拒绝
    with pytest.raises(FollowUpValidationError):
        p.add_followup_step(plan.plan_id, "nonexistent.tool.v9", "observability.logs.read", [])


def test_followup_validates_capability():
    from planner import FollowUpValidationError

    p = _planner()
    plan = p.propose_plan(_intent(), _steps())
    # capability 与 tool.required_capability 不匹配 → 拒绝
    with pytest.raises(FollowUpValidationError):
        p.add_followup_step(plan.plan_id, "query_logs.v1", "observability.metrics.read", [])


def test_followup_shared_budget_exceeded():
    from planner import FollowUpValidationError

    # max_steps=1：初始 1 step 恰好占满 max_steps，follow-up 追加会超 max_steps → 拒绝
    p = _planner(max_steps=1)
    plan = p.propose_plan(_intent(), _steps())
    assert plan.budget.consumed_steps == 1  # 初始 1 step 占满
    with pytest.raises(FollowUpValidationError):
        p.add_followup_step(plan.plan_id, "query_metrics.v1", "observability.metrics.read", [])


def test_followup_respects_max_total_steps():
    from planner import FollowUpValidationError

    # max_total_steps=2：1 初始 + 1 follow-up 允许；第 2 个 follow-up 拒绝
    p = _planner(max_steps=10, max_tools=20)
    p.max_total_steps = 2
    plan = p.propose_plan(_intent(), _steps())
    p.add_followup_step(plan.plan_id, "query_metrics.v1", "observability.metrics.read", [])
    with pytest.raises(FollowUpValidationError):
        p.add_followup_step(plan.plan_id, "query_traces.v1", "observability.traces.read", [])


def test_followup_respects_max_rounds():
    from planner import FollowUpValidationError

    p = _planner(max_steps=20, max_tools=20)
    p.max_followup_rounds = 1
    plan = p.propose_plan(_intent(), _steps())
    p.add_followup_step(plan.plan_id, "query_metrics.v1", "observability.metrics.read", [])
    # 第 2 轮 follow-up → 拒绝
    with pytest.raises(FollowUpValidationError):
        p.add_followup_step(plan.plan_id, "query_traces.v1", "observability.traces.read", [])


def test_followup_does_not_open_second_graph():
    p = _planner()
    plan = p.propose_plan(_intent(), _steps())
    p.add_followup_step(plan.plan_id, "query_metrics.v1", "observability.metrics.read", [])
    # follow-up 并入同一 plan（同一 DAG），不产生第二个 plan/调查图
    assert p.get(plan.plan_id) is plan
    assert len(p._plans) == 1
