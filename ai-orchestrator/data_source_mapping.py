"""P7.9 Registered Data Source Mapping — V9.3 Phase7 复用 V9.2 既有数据源映射。

核心原则：
- 不创建新的 source 身份或授权体系；复用 V9.2 Cluster Registry / tenant_clusters /
  credential_ref / canonical cluster 映射。
- 平台自身 + 外部平台使用相同 ToolResult 状态 / Evidence provenance。
- 未知/未注册 Cluster、无有效配置 → fail-closed。
- 需新 SoT / 新身份权威 / 第二查询路径 → BLOCKED + Architecture Deviation（不静默实现）。
"""
from __future__ import annotations

from typing import FrozenSet

# Tool → Data Source（平台自身）
TOOL_TO_SOURCE = {
    "query_metrics.v1": "VM",
    "query_logs.v1": "VLogs",
    "query_traces.v1": "query-api",
    "query_alerts.v1": "query-api",
    "query_k8s_events.v1": "query-api",
    "query_topology.v1": "query-api",
    "query_changes.v1": "query-api",
    "query_k8s.v1": "query-api",
    "knowledge_search.v1": "query-api",
}

# capability → Data Source
CAPABILITY_TO_SOURCE = {
    "observability.metrics.read": "VM",
    "observability.logs.read": "VLogs",
    "observability.traces.read": "query-api",
    "observability.alerts.read": "query-api",
    "kubernetes.events.read": "query-api",
    "observability.topology.read": "query-api",
    "changes.read": "query-api",
    "kubernetes.resources.read": "query-api",
    "knowledge.search": "query-api",
}


class ClusterFailClosed(ValueError):
    def __init__(self, message: str):
        self.error_code = "CLUSTER_FAIL_CLOSED"
        super().__init__(message)


class ArchitectureDeviation(ValueError):
    def __init__(self, message: str):
        self.error_code = "BLOCKED"
        super().__init__(message)


class DataSourceMapping:
    """注册数据源映射（内存 MVP）。复用 V9.2 既有模型，不新增 SoT。"""

    def __init__(self, registered_clusters: FrozenSet[str] = frozenset()) -> None:
        self._registered_clusters = set(registered_clusters)

    def map_tool(self, tool_id: str) -> str:
        """Tool → Data Source。未知/未注册 Tool → fail-closed（拒绝，不默认回退 query-api）。

        审计 P7 修复：此前 `TOOL_TO_SOURCE.get(tool_id, "query-api")` 对未知 tool 默认
        回退到 query-api（fail-open），docstring 却声称 fail-closed，注释与实现不一致。
        现改为未知 tool 抛 ClusterFailClosed，与 resolve_cluster 的 fail-closed 语义一致，
        避免未来接入流水线时把未注册 tool 静默路由到 query-api。
        """
        if tool_id not in TOOL_TO_SOURCE:
            raise ClusterFailClosed(f"fail-closed: 未注册/未知 tool: {tool_id}")
        return TOOL_TO_SOURCE[tool_id]

    def map_capability(self, capability: str) -> str:
        """capability → Data Source。未知 capability → fail-closed（拒绝，不默认回退 query-api）。

        审计 P7 修复：与 map_tool 同理，未知 capability 抛 ClusterFailClosed。
        """
        if capability not in CAPABILITY_TO_SOURCE:
            raise ClusterFailClosed(f"fail-closed: 未注册/未知 capability: {capability}")
        return CAPABILITY_TO_SOURCE[capability]

    def resolve_cluster(self, cluster_id: str) -> str:
        """Cluster Registry 解析：已注册 canonical → 返回；未知 → fail-closed。"""
        if cluster_id in self._registered_clusters:
            return cluster_id
        raise ClusterFailClosed(f"fail-closed: 未注册/未知 cluster: {cluster_id}")

    def assert_valid_config(self, *, credential_ref: str, cluster_registered: bool) -> None:
        """无有效配置 → fail-closed（拒绝访问，不静默降级）。"""
        if not cluster_registered:
            raise ClusterFailClosed("fail-closed: cluster 未注册")
        if not credential_ref:
            raise ClusterFailClosed("fail-closed: 无有效 credential_ref")

    def classify_model(self, *, needs_new_sot: bool, missing_mandatory_fields: int) -> str:
        """模型充足性判定：reuse | minimal_extension | blocked。"""
        if needs_new_sot:
            return "blocked"
        if missing_mandatory_fields > 0:
            return "minimal_extension"
        return "reuse"

    def assert_not_blocked(self, *, needs_new_sot: bool) -> None:
        """需新 SoT / 新身份 / 第二查询路径 → BLOCKED + Architecture Deviation。"""
        if needs_new_sot:
            raise ArchitectureDeviation(
                "需新 SoT / 新身份权威 / 第二查询路径 → BLOCKED + Architecture Deviation（不静默实现）"
            )
