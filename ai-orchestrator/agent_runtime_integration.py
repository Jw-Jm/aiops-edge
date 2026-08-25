"""ARI.1/2 Agent 集成 InternalQueryClient + TrustedRequestContext — V9.3 Agent Runtime Integration。

把 Agent 的 tool_executor 从"内存 mock"替换为真实 InternalQueryClient（P7.2）：
- 每次调用经 InternalQueryClient → TrustedRequestContext V2 → query-api /internal/v1/query/*。
- capability 精确（client 从 Tool Registry 读，Agent 不自选）。
- 唯一事实路径：Agent 只经 query-api（禁 direct DB/K8s）。
- 未注册 Tool 拒绝。
"""
from __future__ import annotations

from typing import Any, Callable, Dict

from internal_query_client import InternalQueryClient
from tool_registry import ToolRegistry
from tool_execution_context import ToolExecutionContext

# tool_id → operation（对齐 OPERATION_ROUTES）
_TOOL_OPERATION = {
    "query_metrics.v1": "metrics",
    "query_logs.v1": "logs",
    "query_traces.v1": "traces",
    "query_alerts.v1": "alerts",
    "query_topology.v1": "topology",
    "query_k8s.v1": "kubernetes",
    "query_changes.v1": "changes",
    "knowledge_search.v1": "knowledge",
}


class RealToolExecutor:
    """Agent 的真实 Tool 执行器（经 InternalQueryClient → query-api）。"""

    def __init__(self, client: InternalQueryClient, registry=None) -> None:
        self._client = client
        self._registry = registry or ToolRegistry

    def __call__(
        self,
        params: Dict[str, Any],
        *,
        tool_id: str,
        tenant_id: str,
        cluster_id: str,
        context: Dict[str, Any],
    ):
        """执行一个 Tool：校验注册 → operation 映射 → InternalQueryClient.query()。"""
        tool = self._registry.get(tool_id)
        if tool is None or tool.lifecycle_status != "active":
            raise ValueError(f"未注册/非 active Tool: {tool_id}")
        operation = self._tool_operation(tool_id)
        execution_context = None
        if context.get("workload_kind") == "investigation":
            execution_context = ToolExecutionContext.from_mapping(
                context, tool_id=tool_id, params=params,
            )
        return self._client.query(
            tool_id=tool_id,
            operation=operation,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            params=params,
            context_ref=context.get("request_id", ""),
            execution_context=execution_context,
        )

    @staticmethod
    def _tool_operation(tool_id: str) -> str:
        op = _TOOL_OPERATION.get(tool_id)
        if op is None:
            raise ValueError(f"未知 Tool 的 operation 映射: {tool_id}")
        return op
