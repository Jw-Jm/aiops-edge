"""EX.6 Policy 真实 SoT 接入（R3）— TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.6：
- T1 authorization_sot 从 MySQL SoT 读取（V9.3 Authorization SoT）
- T2 cluster_state 经 query-api 只读（无旁路，不直连集群）
- T3 LLM/Agent/Frontend context 仍拒绝（P8.4 冻结延续）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from execution_policy import ExecutionPolicyEngine, PolicyContextInvalid, PolicyRule
from sot_provider import AuthorizationSoTProvider, ClusterStateProvider


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


CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"


@pytest.fixture
def engine():
    return ExecutionPolicyEngine(
        rules=[PolicyRule(policy_id="pol-rate", policy_type="rate_limit", allowed_values=[], denied_values=[], limit=3, scope="")]
    )


@pytest.fixture
def soT_providers():
    sot = AuthorizationSoTProvider(
        {"91771a6e-9c2d-11f1-8271-bea176fe9f9f": {"enabled": True, "capabilities": ["observability.logs.read"]}}
    )
    cluster = ClusterStateProvider(
        {"91771a6e-9c2d-11f1-8271-bea176fe9f9f": {"impact_pods": 2}}
    )
    return sot, cluster


# ═══════════════════════════════════════════════════════
#  T1 authorization_sot 从 MySQL SoT 读取
# ═══════════════════════════════════════════════════════

class TestT1AuthSot:
    def test_auth_from_mysql_sot(self, soT_providers):
        sot, _ = soT_providers
        auth = sot.load_authorization(CLUSTER)
        assert auth["enabled"] is True
        assert "observability.logs.read" in auth["capabilities"]

    def test_policy_context_from_sot(self, contract, engine, soT_providers):
        sot, cluster = soT_providers
        auth = sot.load_authorization(CLUSTER)
        state = cluster.load_cluster_state(CLUSTER)
        # Policy Context 从 SoT provider 构造（非人为）
        ctx = {
            "current_time": _now(),
            "cluster_state": state,
            "execution_history": {"count_1h": 1},
            "authorization_sot": auth,
        }
        assert engine.evaluate(contract, ctx).decision == "ALLOW"


# ═══════════════════════════════════════════════════════
#  T2 cluster_state 经 query-api（无旁路）
# ═══════════════════════════════════════════════════════

class TestT2ClusterViaQueryApi:
    def test_cluster_state_via_query_api(self, soT_providers):
        _, cluster = soT_providers
        state = cluster.load_cluster_state(CLUSTER)
        assert state["impact_pods"] == 2
        # 无旁路：provider 只经 query-api（无直连集群路径）
        assert not hasattr(cluster, "kubectl")
        assert not hasattr(cluster, "direct_connect")


# ═══════════════════════════════════════════════════════
#  T3 LLM/Agent/Frontend context 仍拒绝
# ═══════════════════════════════════════════════════════

class TestT3StillReject:
    def test_llm_context_rejected(self, contract, engine):
        ctx = {"current_time": _now(), "llm_output": {"allow": True}}
        with pytest.raises(PolicyContextInvalid):
            engine.evaluate(contract, ctx)

    def test_agent_suggestion_rejected(self, contract, engine):
        ctx = {"current_time": _now(), "agent_suggestion": "go"}
        with pytest.raises(PolicyContextInvalid):
            engine.evaluate(contract, ctx)
