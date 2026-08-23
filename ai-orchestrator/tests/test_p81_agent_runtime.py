"""P8.1 Agent Runtime Framework — TDD 测试（V9.3 Phase 8 七类 Agent + Resource Graph）。

覆盖合同 P8.1：
- 统一执行 PlanStep → validate scope/budget → select registered Tool → normalize ToolResult → Evidence Hub → MissingEvidence → return Planner
- Agent 不保留第二状态机；无 direct DB/K8s client；禁止 final root cause
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from agent_runtime import AgentOutput, AgentRuntimeFramework, BudgetExceeded
from evidence_hub import EvidenceHub
from internal_query_client import InternalQueryError, QueryResult
from tool_registry import ToolRegistry, init_default_tool_registry
from tool_result import normalize_tool_result


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


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def framework():
    return AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())


def _tool_executor(status=200, body=None):
    # 统一执行接口（审计 P0-4）：executor 接受 (params, *, tool_id, tenant_id, cluster_id, context)
    if status == 200:
        return lambda params, **kw: QueryResult(http_status=200, body=(body or {"logs": [{"ts": 1}], "total": 1}))
    if status == 403:
        return lambda params, **kw: InternalQueryError(kind="permission_denied", http_status=403, message="denied")
    if status == 503:
        return lambda params, **kw: InternalQueryError(kind="unavailable", http_status=503, message="vm down")
    if status == 504:
        return lambda params, **kw: InternalQueryError(kind="timeout", http_status=504, message="slow")
    return lambda params, **kw: QueryResult(http_status=200, body={"error": "NO_DATA", "message": "no data"})


# ═══════════════════════════════════════════════════════
#  T1 统一执行 PlanStep
# ═══════════════════════════════════════════════════════

class TestT1UnifiedExecution:
    def test_execute_step_produces_toolresult_and_evidence(self, framework):
        out = framework.execute_step(
            tool_id="query_logs.v1",
            params={"service": "checkout"},
            tenant_id=TENANT,
            cluster_id=CLUSTER,
            context={"run_id": "run-1"},
            evidence_type="log_pattern",
            tool_executor=_tool_executor(200),
        )
        assert isinstance(out, AgentOutput)
        assert out.tool_results
        assert out.tool_results[0].status == "success"
        assert out.evidence  # 已落 Evidence Hub
        assert out.missing_evidence == []


# ═══════════════════════════════════════════════════════
#  T2 validate scope：未注册 Tool
# ═══════════════════════════════════════════════════════

class TestT2ScopeValidate:
    def test_unregistered_tool_rejected(self, framework):
        with pytest.raises(ValueError):
            framework.execute_step(
                tool_id="evil_tool.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
                context={}, evidence_type="log_pattern", tool_executor=_tool_executor(200),
            )


# ═══════════════════════════════════════════════════════
#  T3 validate budget
# ═══════════════════════════════════════════════════════

class TestT3Budget:
    def test_budget_exceeded_rejected(self):
        fw = AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub(), max_steps=1, max_tools=1)
        fw.execute_step(
            tool_id="query_logs.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, evidence_type="log_pattern", tool_executor=_tool_executor(200),
        )
        with pytest.raises(BudgetExceeded):
            fw.execute_step(
                tool_id="query_logs.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
                context={}, evidence_type="log_pattern", tool_executor=_tool_executor(200),
            )


# ═══════════════════════════════════════════════════════
#  T4 MissingEvidence（no_data / unavailable）
# ═══════════════════════════════════════════════════════

class TestT4MissingEvidence:
    def test_no_data_creates_missing_evidence(self, framework):
        out = framework.execute_step(
            tool_id="query_logs.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, evidence_type="log_pattern", tool_executor=_tool_executor(200, {"error": "NO_DATA"}),
        )
        assert out.tool_results[0].status == "no_data"
        assert out.missing_evidence  # no_data → missing_evidence slot

    def test_unavailable_creates_missing_evidence(self, framework):
        out = framework.execute_step(
            tool_id="query_metrics.v1", params={"service": "checkout"}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, evidence_type="metric_anomaly", tool_executor=_tool_executor(503),
        )
        assert out.tool_results[0].status == "unavailable"
        assert out.missing_evidence  # unavailable → missing_evidence


# ═══════════════════════════════════════════════════════
#  T5 Agent 不保留第二状态机 / 无 final root cause
# ═══════════════════════════════════════════════════════

class TestT5NoSecondStateMachine:
    def test_agent_output_no_root_cause(self, framework):
        out = framework.execute_step(
            tool_id="query_logs.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, evidence_type="log_pattern", tool_executor=_tool_executor(200),
        )
        assert not hasattr(out, "root_cause")  # Agent 禁止 final root cause
        assert not hasattr(framework, "planner_state")  # 无第二状态机
