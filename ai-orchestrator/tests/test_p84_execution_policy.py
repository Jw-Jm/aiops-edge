"""P8.4 Runtime Policy — TDD 测试（V9.3 Phase8，内存 MVP）。

覆盖 P8.4 设计 v0.2 的 T1-T7：
- T1 时间窗口外 → DENY
- T2 资源范围外 → DENY
- T3 动作范围外（delete 被禁）→ DENY
- T4 次数超限 → DENY
- T5 影响范围超限 → DENY
- T6 DENY 不降级为 ALLOW
- T7 Policy Context 来源冻结（LLM/Agent/Frontend 提供的 context → 拒绝）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from execution_policy import (
    ExecutionPolicyEngine,
    PolicyContextInvalid,
    PolicyDecision,
    PolicyRule,
)


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


def _engine():
    return ExecutionPolicyEngine(
        rules=[
            PolicyRule(policy_id="pol-time", policy_type="time_window", allowed_values=[], denied_values=[], limit=0, scope=""),
            PolicyRule(policy_id="pol-res", policy_type="resource_scope", allowed_values=["ns-a"], denied_values=[], limit=0, scope=""),
            PolicyRule(policy_id="pol-act", policy_type="action_scope", allowed_values=[], denied_values=["delete"], limit=0, scope=""),
            PolicyRule(policy_id="pol-rate", policy_type="rate_limit", allowed_values=[], denied_values=[], limit=3, scope=""),
            PolicyRule(policy_id="pol-impact", policy_type="impact_limit", allowed_values=[], denied_values=[], limit=5, scope=""),
        ]
    )


def _ctx(**over):
    kw = dict(
        current_time=_now(),
        cluster_state={"healthy": True},
        execution_history={"count_1h": 1},
        authorization_sot={"enabled": True},
    )
    kw.update(over)
    return kw


# ═══════════════════════════════════════════════════════
#  T1-T6 各类检查
# ═══════════════════════════════════════════════════════

class TestT1_6PolicyChecks:
    def test_t1_time_window_outside_deny(self, contract):
        # 时间窗口 rule：凌晨(0-6) 禁止 → current_time 凌晨 → DENY
        engine = ExecutionPolicyEngine(rules=[PolicyRule(policy_id="pol-time", policy_type="time_window", allowed_values=[], denied_values=[0, 1, 2, 3, 4, 5], limit=0, scope="")])
        ctx = _ctx(current_time=_now().replace(hour=3))
        assert engine.evaluate(contract, ctx).decision == "DENY"

    def test_t2_resource_outside_deny(self, contract):
        engine = _engine()
        # ns-b 不在 pol-res.allowed_values=["ns-a"]
        ctx = _ctx()
        contract2 = contract.__class__(
            **{**contract.__dict__, "contract_id": contract.contract_id, "allowed_resources": ["ns-b"]}
        )
        assert engine.evaluate(contract2, ctx).decision == "DENY"

    def test_t3_action_delete_deny(self, contract):
        engine = _engine()
        c2 = contract.__class__(**{**contract.__dict__, "allowed_actions": ["delete"]})
        assert engine.evaluate(c2, _ctx()).decision == "DENY"

    def test_t4_rate_limit_exceeded(self, contract):
        engine = _engine()
        ctx = _ctx(execution_history={"count_1h": 5})  # limit=3
        assert engine.evaluate(contract, ctx).decision == "DENY"

    def test_t5_impact_limit_exceeded(self, contract):
        engine = _engine()
        ctx = _ctx(cluster_state={"impact_pods": 10})  # limit=5
        assert engine.evaluate(contract, ctx).decision == "DENY"

    def test_t6_all_pass_allow(self, contract):
        engine = _engine()
        assert engine.evaluate(contract, _ctx()).decision == "ALLOW"

    def test_t6_deny_not_downgraded(self, contract):
        engine = _engine()
        ctx = _ctx(execution_history={"count_1h": 5})
        d = engine.evaluate(contract, ctx)
        assert d.decision == "DENY"  # 不降级为 ALLOW
        assert d.reason
        assert d.policy_id


# ═══════════════════════════════════════════════════════
#  T7 Policy Context 来源冻结
# ═══════════════════════════════════════════════════════

class TestT7ContextSource:
    def test_llm_output_rejected(self, contract):
        engine = _engine()
        ctx = _ctx(llm_output={"suggest": "allow"})  # LLM 提供 context → 拒绝
        with pytest.raises(PolicyContextInvalid):
            engine.evaluate(contract, ctx)

    def test_agent_suggestion_rejected(self, contract):
        engine = _engine()
        ctx = _ctx(agent_suggestion="restart")  # Agent 提供 context → 拒绝
        with pytest.raises(PolicyContextInvalid):
            engine.evaluate(contract, ctx)

    def test_frontend_param_rejected(self, contract):
        engine = _engine()
        ctx = _ctx(frontend_param={"override": True})
        with pytest.raises(PolicyContextInvalid):
            engine.evaluate(contract, ctx)

    def test_authority_context_allowed(self, contract):
        engine = _engine()
        assert engine.evaluate(contract, _ctx()).decision in {"ALLOW", "DENY"}  # 权威来源可评估
