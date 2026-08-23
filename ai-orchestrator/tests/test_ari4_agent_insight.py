"""ARI.4 AgentInsight 协议冻结（N3）— TDD 测试（V9.3 Agent Runtime Integration）。

覆盖：
- T1 AgentInsight 统一协议字段（agent_type/evidence_refs/insights/confidence/missing_slots）
- T2 agent_type 校验（非法 → 拒绝）
- T3 confidence 语义（0-1，表达 evidence_confidence，不表达根因正确概率；无 root_cause）
- T4 missing_slots 与 Planner follow-up 关联
- T5 BaseAgent.run 返回统一 AgentInsight（禁止各 Agent 自定义输出）
"""
from __future__ import annotations

import pytest

from agent_insight import AgentInsight, AgentInsightError, AGENT_TYPES
from agent_runtime import AgentRuntimeFramework
from agents import LogAgent, ObservabilityAgent
from evidence_hub import EvidenceHub
from internal_query_client import QueryResult
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


# ═══════════════════════════════════════════════════════
#  T1 统一协议字段
# ═══════════════════════════════════════════════════════

class TestT1Protocol:
    def test_insight_fields(self):
        insight = AgentInsight(
            agent_type="log", evidence_refs=["ev-1"], insights=[{"pattern": "timeout"}],
            confidence=0.8, missing_slots=[],
        )
        assert insight.agent_type == "log"
        assert insight.evidence_refs == ["ev-1"]
        assert insight.insights == [{"pattern": "timeout"}]
        assert insight.confidence == 0.8
        assert insight.missing_slots == []


# ═══════════════════════════════════════════════════════
#  T2 agent_type 校验
# ═══════════════════════════════════════════════════════

class TestT2AgentType:
    def test_invalid_agent_type_rejected(self):
        with pytest.raises(AgentInsightError):
            AgentInsight(
                agent_type="unknown", evidence_refs=[], insights=[], confidence=0.5, missing_slots=[]
            )

    def test_all_types_valid(self):
        for at in AGENT_TYPES:
            AgentInsight(agent_type=at, evidence_refs=[], insights=[], confidence=0.5, missing_slots=[])


# ═══════════════════════════════════════════════════════
#  T3 confidence 语义（evidence_confidence，非根因概率）
# ═══════════════════════════════════════════════════════

class TestT3ConfidenceSemantics:
    def test_confidence_in_range(self):
        insight = AgentInsight(agent_type="log", evidence_refs=[], insights=[], confidence=0.7, missing_slots=[])
        assert 0.0 <= insight.confidence <= 1.0

    def test_no_root_cause(self):
        # confidence 不表达根因正确概率；AgentInsight 无 root_cause 字段
        insight = AgentInsight(agent_type="log", evidence_refs=[], insights=[], confidence=0.7, missing_slots=[])
        assert not hasattr(insight, "root_cause")

    def test_confidence_out_of_range_rejected(self):
        with pytest.raises(AgentInsightError):
            AgentInsight(agent_type="log", evidence_refs=[], insights=[], confidence=1.5, missing_slots=[])


# ═══════════════════════════════════════════════════════
#  T4 missing_slots 与 Planner follow-up
# ═══════════════════════════════════════════════════════

class TestT4MissingSlots:
    def test_missing_slots_struct(self):
        insight = AgentInsight(
            agent_type="log", evidence_refs=[], insights=[],
            confidence=0.5, missing_slots=[{"tool_id": "query_metrics.v1", "capability": "observability.metrics.read", "reason": "no_data"}],
        )
        assert insight.missing_slots[0]["tool_id"] == "query_metrics.v1"


# ═══════════════════════════════════════════════════════
#  T5 BaseAgent.run 返回统一 AgentInsight
# ═══════════════════════════════════════════════════════

class TestT5UnifiedOutput:
    def test_agent_insight_via_to_insight(self):
        fw = AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())
        agent = ObservabilityAgent(fw)
        out = agent.run(
            params={"service": "checkout"}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=lambda p, **kw: QueryResult(200, {"points": [{"abnormal": True}], "total": 1}),
        )
        insight = agent.to_insight(out)
        assert isinstance(insight, AgentInsight)
        assert insight.agent_type == "observability"

    def test_log_agent_insight_type(self):
        fw = AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())
        agent = LogAgent(fw)
        out = agent.run(
            params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, tool_executor=lambda p, **kw: QueryResult(200, {"logs": [{"ts": 1}], "total": 1}),
        )
        insight = agent.to_insight(out)
        assert insight.agent_type == "log"
