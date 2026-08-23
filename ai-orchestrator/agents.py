"""P8.2-P8.8 七类 Agent — V9.3 Phase 8。

所有 Agent 共用 P8.1 AgentRuntimeFramework 契约：
  INPUT:  PlanStep, Context, Existing Evidence
  OUTPUT: ToolResult[], Evidence[], MissingEvidence[]

原则：
- 每类 Agent 绑定一个已注册 Tool（经 Tool Registry → query-api，无 direct DB/K8s client）。
- 领域分析（analyze）只提取"事实"证据，不产出 final root cause。
- no_data / unavailable 严格区分（不伪装 healthy）。
- Log Agent 仅用户手动发起的 Run 内按 Planner DAG 自动参与。
"""
from __future__ import annotations

from typing import Any, Callable, Dict, List, Optional

from agent_insight import AgentInsight
from agent_runtime import AgentOutput, AgentRuntimeFramework


class BaseAgent:
    agent_id = ""
    required_tool_id = ""
    evidence_type = ""
    capability = ""

    def __init__(self, framework: AgentRuntimeFramework) -> None:
        self._framework = framework

    def run(
        self,
        *,
        params: Dict[str, Any],
        tenant_id: str,
        cluster_id: str,
        context: Dict[str, Any],
        tool_executor: Callable,
        existing_evidence: Optional[List[Any]] = None,
    ) -> AgentOutput:
        """统一执行契约：经 framework 执行绑定 Tool，返回 AgentOutput（Phase 8 兼容）。"""
        return self._framework.execute_step(
            tool_id=self.required_tool_id,
            params=params,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            context=context,
            evidence_type=self.evidence_type,
            tool_executor=tool_executor,
        )

    def to_insight(self, out: AgentOutput) -> AgentInsight:
        """把 AgentOutput 转为统一 AgentInsight（N3 协议）。"""
        evidence_refs = [getattr(ev, "evidence_id", "") for ev in out.evidence]
        return AgentInsight(
            agent_type=self.agent_id,
            evidence_refs=evidence_refs,
            insights=self._insights(out),
            confidence=self._confidence(out),
            missing_slots=list(out.missing_evidence),
        )

    def _to_insight(self, out: AgentOutput) -> AgentInsight:
        evidence_refs = [getattr(ev, "evidence_id", "") for ev in out.evidence]
        return AgentInsight(
            agent_type=self.agent_id,
            evidence_refs=evidence_refs,
            insights=self._insights(out),
            confidence=self._confidence(out),
            missing_slots=list(out.missing_evidence),
        )

    def _insights(self, out: AgentOutput) -> List[Dict[str, Any]]:
        """领域分析：提取事实证据（不归因）。子类可覆写。"""
        return []

    def _confidence(self, out: AgentOutput) -> float:
        """evidence_confidence（证据置信度，非根因概率）。有证据高、缺证据低。"""
        if out.evidence:
            return 0.8
        if out.missing_evidence:
            return 0.3
        return 0.5


class ObservabilityAgent(BaseAgent):
    """P8.2：metrics/RED/SLI/SLO/current-vs-baseline；输出 first abnormal timestamp/delta。"""

    agent_id = "observability"
    required_tool_id = "query_metrics.v1"
    evidence_type = "metric_anomaly"
    capability = "observability.metrics.read"

    def _insights(self, out):
        insights = []
        if out.tool_results and out.tool_results[0].status == "success":
            data = out.tool_results[0].data or {}
            points = data.get("points") or []
            abnormal = [p for p in points if p.get("abnormal")]
            if abnormal:
                first = abnormal[0]
                insights.append(
                    {
                        "kind": "first_abnormal",
                        "first_abnormal_timestamp": first.get("ts"),
                        "delta": first.get("delta"),
                    }
                )
        return insights


class LogAgent(BaseAgent):
    """P8.3：raw logs/pattern/keyword-error trend/service correlation。仅 Run 内按 DAG 参与。"""

    agent_id = "log"
    required_tool_id = "query_logs.v1"
    evidence_type = "log_pattern"
    capability = "observability.logs.read"


class TraceAgent(BaseAgent):
    """P8.4：slow/error trace/critical span/dependency path/trace→logs-service linkage。"""

    agent_id = "trace"
    required_tool_id = "query_traces.v1"
    evidence_type = "trace_anomaly"
    capability = "observability.traces.read"


class KubernetesAgent(BaseAgent):
    """P8.5：workload/pod/node/events/restarts/OOM/CrashLoop/pressure；仅经 Tool→query-api。"""

    agent_id = "kubernetes"
    required_tool_id = "query_k8s.v1"
    evidence_type = "k8s_state"
    capability = "kubernetes.resources.read"


class ChangeAgent(BaseAgent):
    """P8.6：deployment/config/scale/restart/resource-event → change timeline；不得默认"最近变更=根因"。"""

    agent_id = "change"
    required_tool_id = "query_changes.v1"
    evidence_type = "change"
    capability = "changes.read"

    def _insights(self, out):
        if out.tool_results and out.tool_results[0].status == "success":
            data = out.tool_results[0].data or {}
            timeline = data.get("timeline") or []
            # 只组织 change timeline，不默认最近变更=根因
            return [{"kind": "change_timeline", "change_timeline": timeline, "is_root_cause": False}]
        return []


class KnowledgeAgent(BaseAgent):
    """P8.7：Chroma/MinIO；无结果严格 no_data；Runbook/SOP 只作知识证据，不冒充 live fact。"""

    agent_id = "knowledge"
    required_tool_id = "knowledge_search.v1"
    evidence_type = "knowledge_case"
    capability = "knowledge.search"


class InfrastructureAgent(BaseAgent):
    """P8.8：node hardware/SEL/sensor/capacity/host；缺 sensor→unknown、不可达→unavailable、不假装 healthy。"""

    agent_id = "infrastructure"
    required_tool_id = "query_topology.v1"
    evidence_type = "resource_state"
    capability = "observability.topology.read"
