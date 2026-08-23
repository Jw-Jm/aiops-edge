"""P8.9 Resource Graph V1 — TDD 测试（V9.3 Phase 8）。

覆盖：
- T1 typed node/edge 添加
- T2 traversal 权限过滤（跨 tenant/cluster 节点不返回）
- T3 默认禁止跨 Cluster edge（跨 cluster 节点不泄漏）
- T4 traversal 只返回同 cluster + 授权范围内节点
"""
from __future__ import annotations

import pytest

from resource_graph import GraphEdge, GraphNode, ResourceGraph, TraversalDenied


CLUSTER_A = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
CLUSTER_B = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"


@pytest.fixture
def graph():
    g = ResourceGraph()
    # cluster A: svc -> pod
    g.add_node(GraphNode(node_id="svc-checkout", type="service", cluster_id=CLUSTER_A, tenant_id=TENANT))
    g.add_node(GraphNode(node_id="pod-checkout-0", type="pod", cluster_id=CLUSTER_A, tenant_id=TENANT))
    g.add_edge(GraphEdge(source="svc-checkout", target="pod-checkout-0", type="owns", cluster_id=CLUSTER_A))
    # cluster B: other service（跨 cluster）
    g.add_node(GraphNode(node_id="svc-other", type="service", cluster_id=CLUSTER_B, tenant_id=TENANT))
    g.add_edge(GraphEdge(source="svc-checkout", target="svc-other", type="cross", cluster_id=CLUSTER_A))
    return g


# ═══════════════════════════════════════════════════════
#  T1 typed node/edge
# ═══════════════════════════════════════════════════════

class TestT1Typed:
    def test_nodes_and_edges(self, graph):
        assert graph.node("svc-checkout").type == "service"
        assert graph.node("pod-checkout-0").type == "pod"
        assert len(graph.edges()) == 2


# ═══════════════════════════════════════════════════════
#  T2 traversal 权限过滤
# ═══════════════════════════════════════════════════════

class TestT2PermissionFilter:
    def test_cross_cluster_not_returned(self, graph):
        # 从 svc-checkout traversal，只返回同 cluster(CLUSTER_A) 节点；svc-other(CLUSTER_B) 不泄漏
        neighbors = graph.neighbors("svc-checkout", cluster_id=CLUSTER_A, tenant_id=TENANT)
        ids = [n.node_id for n in neighbors]
        assert "pod-checkout-0" in ids
        assert "svc-other" not in ids  # 跨 cluster 不泄漏


# ═══════════════════════════════════════════════════════
#  T3 禁跨 cluster edge
# ═══════════════════════════════════════════════════════

class TestT3NoCrossCluster:
    def test_cross_edge_skipped(self, graph):
        # 显式声明跨 cluster edge（svc-checkout -> svc-other）在 traversal 中被跳过
        neighbors = graph.neighbors("svc-checkout", cluster_id=CLUSTER_A, tenant_id=TENANT)
        assert all(n.cluster_id == CLUSTER_A for n in neighbors)


# ═══════════════════════════════════════════════════════
#  T4 授权范围内节点
# ═══════════════════════════════════════════════════════

class TestT4AuthorizedScope:
    def test_wrong_tenant_denied(self, graph):
        # 其他 tenant 无权访问 → 拒绝
        with pytest.raises(TraversalDenied):
            graph.neighbors("svc-checkout", cluster_id=CLUSTER_A, tenant_id="other-tenant")
