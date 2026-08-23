"""P8.2-P8.8 七类 Agent — TDD 测试（V9.3 Phase 8）。

覆盖：
- 每类 Agent 选择正确 Tool（required_tool_id / capability）
- success → Evidence；no_data / unavailable 严格区分（MissingEvidence）
- 领域 insight（Observability first abnormal timestamp；Change timeline 不默认根因）
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from agent_runtime import AgentRuntimeFramework
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


def _executor_for(tool_id):
    def _exec(params, **kw):
        if tool_id == "query_metrics.v1":
            return QueryResult(200, {"points": [{"ts": 100, "abnormal": True, "delta": 0.5}], "total": 1})
        if tool_id == "query_logs.v1":
            return QueryResult(200, {"logs": [{"ts": 1}], "total": 1})
        if tool_id == "query_traces.v1":
            return QueryResult(200, {"traces": [{"id": "t1", "slow": True}], "total": 1})
        if tool_id == "query_k8s.v1":
            return QueryResult(200, {"pods": [{"name": "checkout-0", "crashloop": True}], "total": 1})
        if tool_id == "query_changes.v1":
            return QueryResult(200, {"timeline": [{"at": 100, "type": "deploy"}], "total": 1})
        if tool_id == "knowledge_search.v1":
            return QueryResult(200, {"results": [{"doc": "runbook"}], "total": 1})
        return QueryResult(200, {"nodes": [{"name": "node-1"}], "total": 1})
    return _exec


@pytest.fixture
def framework():
    return AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())


# ═══════════════════════════════════════════════════════
#  T1 每类 Agent 选择正确 Tool
# ═══════════════════════════════════════════════════════

class TestT1ToolSelection:
    def test_observability_uses_metrics(self, framework):
        assert ObservabilityAgent(framework).required_tool_id == "query_metrics.v1"
        assert ObservabilityAgent(framework).capability == "observability.metrics.read"

    def test_all_agents_have_tools(self, framework):
        agents = [
            ObservabilityAgent(framework), LogAgent(framework), TraceAgent(framework),
            KubernetesAgent(framework), ChangeAgent(framework), KnowledgeAgent(framework),
            InfrastructureAgent(framework),
        ]
        for a in agents:
            assert a.required_tool_id
            assert a.evidence_type
            assert isinstance(a, BaseAgent)


# ═══════════════════════════════════════════════════════
#  T2 success → Evidence；no_data/unavailable 区分
# ═══════════════════════════════════════════════════════

class TestT2Semantics:
    def test_observability_success_evidence(self, framework):
        out = ObservabilityAgent(framework).run(
            params={"service": "checkout"}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=_executor_for("query_metrics.v1"),
        )
        assert out.tool_results[0].status == "success"
        assert out.evidence  # success → Evidence

    def test_no_data_strictly(self, framework):
        out = LogAgent(framework).run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=lambda p, **kw: QueryResult(200, {"error": "NO_DATA"}),
        )
        assert out.tool_results[0].status == "no_data"
        assert out.evidence == []  # no_data 不落 Evidence
        assert out.missing_evidence  # no_data → MissingEvidence

    def test_unavailable_strictly(self, framework):
        out = LogAgent(framework).run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=lambda p, **kw: InternalQueryError(kind="unavailable", http_status=503, message="down"),
        )
        assert out.tool_results[0].status == "unavailable"
        assert out.missing_evidence  # unavailable → MissingEvidence（不伪装 healthy）


# ═══════════════════════════════════════════════════════
#  T3 领域 insight
# ═══════════════════════════════════════════════════════

class TestT3Insight:
    def test_observability_first_abnormal(self, framework):
        agent = ObservabilityAgent(framework)
        out = agent.run(
            params={"service": "checkout"}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=_executor_for("query_metrics.v1"),
        )
        insight = agent.to_insight(out)
        assert insight.insights[0]["first_abnormal_timestamp"] == 100
        assert insight.insights[0]["delta"] == 0.5

    def test_change_timeline_not_root_cause(self, framework):
        agent = ChangeAgent(framework)
        out = agent.run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=_executor_for("query_changes.v1"),
        )
        insight = agent.to_insight(out)
        assert insight.insights[0]["is_root_cause"] is False  # 不默认"最近变更=根因"
        assert insight.insights[0]["change_timeline"] == [{"at": 100, "type": "deploy"}]
