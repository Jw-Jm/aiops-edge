"""P9.6 Follow-up Planner — V9.3 Phase9（TDD RED 测试）。

只针对 missing evidence 新增步骤，仍由唯一 Planner 控制并受全局 budget。
follow-up 不能开启第二调查图（§七十五 P9.6）。
"""
import pytest


def test_followup_created_for_missing():
    from followup_planner import FollowUpPlanner

    planner = FollowUpPlanner(max_steps=5)
    req = planner.propose_followup(
        hypothesis_id="h-1",
        missing_id="m-1",
        tool_id="query_k8s",
        capability="kubernetes.read",
        budget_cost=1,
    )
    assert req.status == "proposed"
    assert req.hypothesis_id == "h-1"
    assert req.tool_id == "query_k8s"


def test_followup_consumes_budget():
    from followup_planner import FollowUpPlanner

    planner = FollowUpPlanner(max_steps=2)
    req = planner.propose_followup("h-1", "m-1", "tool-a", "cap", budget_cost=1)
    planner.accept(req.followup_id)
    assert planner.consumed_steps == 1


def test_budget_exceeded_terminates():
    from followup_planner import FollowUpPlanner, BudgetExceededError

    planner = FollowUpPlanner(max_steps=1)
    planner.accept(planner.propose_followup("h-1", "m-1", "tool-a", "cap", budget_cost=1).followup_id)
    with pytest.raises(BudgetExceededError):
        planner.accept(planner.propose_followup("h-1", "m-2", "tool-b", "cap", budget_cost=1).followup_id)


def test_followup_does_not_open_second_graph():
    from followup_planner import FollowUpPlanner

    planner = FollowUpPlanner(max_steps=10)
    # follow-up 始终挂在单一 hypothesis 的单一 planner 上，不产生独立调查图
    assert planner.investigation_graph_id == "primary"  # 唯一图标识


def test_accept_reject_lifecycle():
    from followup_planner import FollowUpPlanner

    planner = FollowUpPlanner(max_steps=10)
    req = planner.propose_followup("h-1", "m-1", "tool-a", "cap", budget_cost=1)
    planner.reject(req.followup_id)
    assert planner.status(req.followup_id) == "rejected"
