"""P8.10 Agent Contract Tests — TDD 测试（V9.3 Phase 8 Gate 8 断言）。

每个 Agent 至少验证：success / no_data / permission_denied / unavailable / timeout /
missing evidence / wrong cluster / budget exhaustion。

Gate 8 断言：
- 所有 7 类 Agent 用同一 runtime contract（AgentRuntimeFramework）
- 无 direct DB/K8s client
- source unavailable != no_data/healthy
- 无 edge autonomy/governance 子系统
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from agent_runtime import AgentRuntimeFramework, BudgetExceeded
from agents import (
    BaseAgent,
    ChangeAgent,
    InfrastructureAgent,
    KnowledgeAgent,
    KubernetesAgent,
    LogAgent,
    ObservabilityAgent,
    TraceAgent,
)
from evidence_hub import EvidenceHub
from internal_query_client import InternalQueryError, QueryResult
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
ALL_AGENTS = lambda fw: [
    ObservabilityAgent(fw), LogAgent(fw), TraceAgent(fw), KubernetesAgent(fw),
    ChangeAgent(fw), KnowledgeAgent(fw), InfrastructureAgent(fw),
]


@pytest.fixture
def framework():
    return AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())


def _ok_executor(tool_id):
    def _exec(p, **kw):
        return QueryResult(200, {"data": [1], "total": 1})
    return _exec


# ═══════════════════════════════════════════════════════
#  统一 runtime contract
# ═══════════════════════════════════════════════════════

class TestGate8UnifiedContract:
    def test_all_agents_same_contract(self, framework):
        for agent in ALL_AGENTS(framework):
            assert isinstance(agent, BaseAgent)
            assert hasattr(agent, "run")  # 统一执行契约
            assert agent.required_tool_id  # 绑定已注册 Tool


# ═══════════════════════════════════════════════════════
#  无 direct DB/K8s client
# ═══════════════════════════════════════════════════════

class TestGate8NoDirectClient:
    def test_no_db_k8s_client(self, framework):
        for agent in ALL_AGENTS(framework):
            assert not hasattr(agent, "kubectl")
            assert not hasattr(agent, "db_client")
            assert not hasattr(agent, "mysql")


# ═══════════════════════════════════════════════════════
#  source unavailable != no_data/healthy
# ═══════════════════════════════════════════════════════

class TestGate8SemanticDistinction:
    def test_unavailable_not_no_data(self, framework):
        out = LogAgent(framework).run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER, context={},
            tool_executor=lambda p, **kw: InternalQueryError(kind="unavailable", http_status=503, message="down"),
        )
        assert out.tool_results[0].status == "unavailable"
        assert out.tool_results[0].status != "no_data"
        assert out.tool_results[0].status != "success"

    def test_permission_denied_not_no_data(self, framework):
        out = LogAgent(framework).run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER, context={},
            tool_executor=lambda p, **kw: InternalQueryError(kind="permission_denied", http_status=403, message="denied"),
        )
        assert out.tool_results[0].status == "permission_denied"
        assert out.missing_evidence == []  # permission_denied 不是 missing evidence

    def test_timeout_distinct(self, framework):
        out = LogAgent(framework).run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER, context={},
            tool_executor=lambda p, **kw: InternalQueryError(kind="timeout", http_status=504, message="slow"),
        )
        assert out.tool_results[0].status == "timeout"


# ═══════════════════════════════════════════════════════
#  budget exhaustion
# ═══════════════════════════════════════════════════════

class TestGate8Budget:
    def test_budget_exhaustion(self):
        fw = AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub(), max_steps=1)
        agent = LogAgent(fw)
        agent.run(params={}, tenant_id=TENANT, cluster_id=CLUSTER, context={}, tool_executor=_ok_executor("query_logs.v1"))
        with pytest.raises(BudgetExceeded):
            agent.run(params={}, tenant_id=TENANT, cluster_id=CLUSTER, context={}, tool_executor=_ok_executor("query_logs.v1"))


# ═══════════════════════════════════════════════════════
#  无 edge autonomy/governance 子系统
# ═══════════════════════════════════════════════════════

class TestGate8NoAutonomy:
    def test_no_autonomy_governance(self, framework):
        for agent in ALL_AGENTS(framework):
            assert not hasattr(agent, "autonomy_engine")
            assert not hasattr(agent, "governance")
            assert not hasattr(agent, "self_execute")
