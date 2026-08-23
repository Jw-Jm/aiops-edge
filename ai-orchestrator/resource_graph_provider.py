"""ARI.3 Resource Graph query-api provider — V9.3 Agent Runtime Integration。

Resource Graph V1 从 query-api 采集（非直连 informer/CMDB）：
- 经 InternalQueryClient → /internal/v1/query/topology。
- 构建 typed graph（GraphNode/GraphEdge），带 tenant/cluster。
- 隔离：cross-cluster node 不泄漏（由 ResourceGraph.neighbors 保证）。
"""
from __future__ import annotations

from typing import Any, Dict

from internal_query_client import InternalQueryClient
from resource_graph import GraphEdge, GraphNode, ResourceGraph


class ResourceGraphProvider:
    """Resource Graph 采集 provider（经 query-api）。"""

    def __init__(self, client: InternalQueryClient) -> None:
        self._client = client

    def fetch_topology(self, *, tenant_id: str, cluster_id: str, context: Dict[str, Any]) -> ResourceGraph:
        """经 query-api 采集拓扑，构建 typed graph。"""
        result = self._client.query(
            tool_id="query_topology.v1",
            operation="topology",
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            params={},
            context_ref=context.get("request_id", ""),
        )
        body = result.body or {}
        graph = ResourceGraph()
        for n in body.get("nodes", []):
            graph.add_node(
                GraphNode(
                    node_id=n.get("id", ""),
                    type=n.get("type", ""),
                    cluster_id=n.get("cluster", cluster_id),
                    tenant_id=tenant_id,
                )
            )
        for e in body.get("edges", []):
            graph.add_edge(
                GraphEdge(
                    source=e.get("source", ""),
                    target=e.get("target", ""),
                    type=e.get("relation", "related"),
                    cluster_id=cluster_id,
                )
            )
        return graph
