"""P8.9 Resource Graph V1 — V9.3 Phase 8 typed resource graph + 权限过滤。

原则：
- typed node/edge（cluster/namespace/service/pod/...）。
- 每次 traversal 先权限过滤（tenant/cluster/capability）。
- 默认禁止跨 Cluster edge（跨 cluster 节点不泄漏）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


class TraversalDenied(ValueError):
    def __init__(self, message: str):
        self.error_code = "TRAVERSAL_DENIED"
        super().__init__(message)


@dataclass
class GraphNode:
    node_id: str
    type: str
    cluster_id: str
    tenant_id: str
    labels: Dict[str, Any] = field(default_factory=dict)


@dataclass
class GraphEdge:
    source: str
    target: str
    type: str
    cluster_id: str = ""


class ResourceGraph:
    """内存 typed Resource Graph V1（MVP）。"""

    def __init__(self) -> None:
        self._nodes: Dict[str, GraphNode] = {}
        self._edges: List[GraphEdge] = []
        self._adj: Dict[str, List[str]] = {}

    def add_node(self, node: GraphNode) -> None:
        self._nodes[node.node_id] = node

    def add_edge(self, edge: GraphEdge) -> None:
        self._edges.append(edge)
        self._adj.setdefault(edge.source, []).append(edge.target)
        self._adj.setdefault(edge.target, []).append(edge.source)

    def node(self, node_id: str) -> Optional[GraphNode]:
        return self._nodes.get(node_id)

    def edges(self) -> List[GraphEdge]:
        return list(self._edges)

    def neighbors(
        self,
        node_id: str,
        *,
        cluster_id: str,
        tenant_id: str,
        capability: Optional[str] = None,
        required_capability: Optional[str] = None,
    ) -> List[GraphNode]:
        """traversal：先权限过滤（tenant + cluster + capability），只返回同 cluster 节点。

        审计 P0-5 修复：必须校验起点 cluster 与请求 cluster 一致，否则从 cluster A 节点
        传入 cluster B 的 scope 可返回 B 的邻居（跨 Cluster pivot）。同时接口接受
        capability 校验（请求者能力 gate，fail-closed）。
        """
        start = self._nodes.get(node_id)
        if start is None:
            raise TraversalDenied(f"node 不存在: {node_id}")
        # 起点权限 fail-closed：tenant + cluster 都必须匹配请求 scope
        if start.tenant_id != tenant_id:
            raise TraversalDenied(f"tenant 无权访问 node: {node_id}")
        if start.cluster_id != cluster_id:
            raise TraversalDenied(
                f"node 跨 cluster pivot 拒绝: start.cluster={start.cluster_id} != scope.cluster={cluster_id}"
            )
        # capability 校验（请求者能力 gate，fail-closed）
        if required_capability and capability != required_capability:
            raise TraversalDenied(
                f"capability 无权访问: {capability!r} != required {required_capability!r}"
            )
        result: List[GraphNode] = []
        for neighbor_id in self._adj.get(node_id, []):
            n = self._nodes.get(neighbor_id)
            if n is None:
                continue
            # 权限过滤：只返回授权 tenant + 同 cluster 节点（默认禁跨 cluster）
            if n.tenant_id != tenant_id:
                continue
            if n.cluster_id != cluster_id:
                continue  # 跨 cluster 不泄漏
            result.append(n)
        return result
