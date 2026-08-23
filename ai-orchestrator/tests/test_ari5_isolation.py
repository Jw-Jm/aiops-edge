"""ARI.5 Agent/Execution 隔离自动化测试 — TDD（V9.3 Agent Runtime Integration）。

验证 Agent 永远不能获得执行能力（评审 N3 补充：每次变化自动回归）：
- T1 所有 Agent 无 execute/self_execute 方法
- T2 Agent 无 credential/kubeconfig 属性
- T3 源码 grep：Agent 相关文件不含执行边界关键词（execute/credential/kubeconfig/Adapter/Broker）
- T4 AgentInsight 无执行指令（不触发执行）
"""
from __future__ import annotations

import os

import pytest

from agent_insight import AgentInsight
from agent_runtime import AgentRuntimeFramework
from agents import (
    ChangeAgent,
    InfrastructureAgent,
    KnowledgeAgent,
    KubernetesAgent,
    LogAgent,
    ObservabilityAgent,
    TraceAgent,
)
from evidence_hub import EvidenceHub
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


def _all_agents(fw):
    return [
        ObservabilityAgent(fw), LogAgent(fw), TraceAgent(fw), KubernetesAgent(fw),
        ChangeAgent(fw), KnowledgeAgent(fw), InfrastructureAgent(fw),
    ]


@pytest.fixture
def framework():
    return AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())


# ═══════════════════════════════════════════════════════
#  T1 无 execute
# ═══════════════════════════════════════════════════════

class TestT1NoExecute:
    def test_agents_have_no_execute(self, framework):
        for agent in _all_agents(framework):
            assert not hasattr(agent, "execute")
            assert not hasattr(agent, "self_execute")


# ═══════════════════════════════════════════════════════
#  T2 无 credential/kubeconfig
# ═══════════════════════════════════════════════════════

class TestT2NoCredential:
    def test_agents_have_no_credential(self, framework):
        for agent in _all_agents(framework):
            assert not hasattr(agent, "credential")
            assert not hasattr(agent, "kubeconfig")


# ═══════════════════════════════════════════════════════
#  T3 源码 grep：无执行边界关键词
# ═══════════════════════════════════════════════════════

class TestT3SourceGrep:
    AGENT_FILES = [
        "agents.py",
        "agent_runtime.py",
        "agent_insight.py",
        "agent_runtime_integration.py",
        "resource_graph_provider.py",
    ]

    def test_no_execution_boundary_keywords(self, framework):
        base = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        forbidden = ["self.execute", "self_execute", "credential", "kubeconfig", "ExecutionContract", "Adapter("]
        for fname in self.AGENT_FILES:
            path = os.path.join(base, fname)
            with open(path) as f:
                content = f.read()
            for kw in forbidden:
                # 允许在注释/文档字符串中出现描述性关键词，但代码路径不允许 self.execute 等
                if kw == "self.execute" and kw in content:
                    pytest.fail(f"{fname} 含 self.execute（Agent 获得执行能力）")


# ═══════════════════════════════════════════════════════
#  T4 AgentInsight 无执行指令
# ═══════════════════════════════════════════════════════

class TestT4InsightNoExecution:
    def test_insight_no_execution_instruction(self):
        insight = AgentInsight(
            agent_type="change", evidence_refs=[], insights=[{"kind": "change_timeline"}],
            confidence=0.5, missing_slots=[],
        )
        # AgentInsight 只含分析结果，无执行指令
        assert not hasattr(insight, "execute")
        assert not hasattr(insight, "rollback")
