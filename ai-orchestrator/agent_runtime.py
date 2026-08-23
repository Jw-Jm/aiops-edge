"""P8.1 Agent Runtime Framework — V9.3 Phase 8 统一 Agent 执行契约。

统一执行链（P8.1）：
  PlanStep → validate scope/budget → select registered Tool → normalize ToolResult
  → Evidence Hub → MissingEvidence → return Planner

原则：
- Agent 不保留第二状态机（调查顺序由 Planner DAG 决定，Agent 只执行单个 PlanStep）。
- 无 direct DB / K8s client（Agent 只能经 Tool Registry → query-api）。
- 禁止 final root cause（Agent 只产出证据，不归因）。
- no_data / unavailable → MissingEvidence slot（不伪装）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Callable, Dict, List, Optional

from evidence_hub import EvidenceHub
from tool_registry import ToolRegistry
from tool_result import ToolResult, normalize_tool_result


class BudgetExceeded(ValueError):
    def __init__(self, message: str):
        self.error_code = "BUDGET_EXCEEDED"
        super().__init__(message)


@dataclass
class AgentOutput:
    tool_results: List[ToolResult] = field(default_factory=list)
    evidence: List[Any] = field(default_factory=list)
    missing_evidence: List[Dict[str, Any]] = field(default_factory=list)


class AgentRuntimeFramework:
    """统一 Agent 执行框架（内存 MVP）。7 类 Agent 共用此契约。"""

    def __init__(
        self,
        *,
        registry=None,
        evidence_hub: Optional[EvidenceHub] = None,
        max_steps: int = 10,
        max_tools: int = 20,
    ) -> None:
        self._registry = registry or ToolRegistry
        self._evidence_hub = evidence_hub or EvidenceHub()
        self._max_steps = max_steps
        self._max_tools = max_tools
        self._consumed_steps = 0
        self._consumed_tools = 0
        self._used_tools: set = set()

    def execute_step(
        self,
        *,
        tool_id: str,
        params: Dict[str, Any],
        tenant_id: str,
        cluster_id: str,
        context: Dict[str, Any],
        evidence_type: str,
        tool_executor: Callable,
    ) -> AgentOutput:
        """执行单个 PlanStep：validate scope/budget → select tool → run → normalize → evidence → missing。"""
        # validate scope：Tool 必须注册且 active（P7.1）
        tool = self._registry.get(tool_id)
        if tool is None or tool.lifecycle_status != "active":
            raise ValueError(f"未注册/非 active Tool: {tool_id}")
        # validate budget
        if self._consumed_steps + 1 > self._max_steps:
            raise BudgetExceeded(f"steps 预算超限: {self._consumed_steps + 1} > {self._max_steps}")
        new_tools = self._consumed_tools + (1 if tool_id not in self._used_tools else 0)
        if new_tools > self._max_tools:
            raise BudgetExceeded(f"tools 预算超限: {new_tools} > {self._max_tools}")

        # run tool（经 query-api，统一执行接口，审计 P0-4）
        # 必须把完整执行上下文传给 executor（tool_id/tenant_id/cluster_id/context），
        # 否则 RealToolExecutor 强制要求的 4 个关键字参数缺失 → TypeError。
        outcome = tool_executor(
            params,
            tool_id=tool_id,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            context=context,
        )
        now = datetime.now(timezone.utc)
        tr = normalize_tool_result(
            outcome=outcome,
            tool=tool,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            request_id=context.get("request_id", ""),
            query_id=context.get("query_id", ""),
            time_range=context.get("time_range", ""),
            source_system="query-api",
            started_at=now,
            finished_at=now,
        )

        # Evidence Hub 落库（P7.4）
        evidence = []
        if tr.status in {"success", "partial"}:
            ev = self._evidence_hub.save_from_tool_result(
                tr,
                run_id=context.get("run_id", ""),
                evidence_type=evidence_type,
            )
            evidence.append(ev)

        # MissingEvidence：no_data / unavailable → slot（不伪装）
        missing = []
        if tr.status in {"no_data", "unavailable"}:
            missing.append(
                {
                    "tool_id": tool_id,
                    "capability": tool.capability,
                    "reason": f"source unavailable/no_data: {tr.status}",
                }
            )

        # 消耗预算
        self._consumed_steps += 1
        self._consumed_tools = new_tools
        self._used_tools.add(tool_id)

        return AgentOutput(tool_results=[tr], evidence=evidence, missing_evidence=missing)
