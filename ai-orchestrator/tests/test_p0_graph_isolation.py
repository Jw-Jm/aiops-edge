"""P0-5 Graph 跨 Cluster pivot 隔离测试（审计阻断项 B0-05）。

此前 neighbors 只检查起点 tenant，未验证起点 cluster 与请求 cluster 相同，
从 cluster A 节点传入 cluster B 的 scope 可返回 B 的邻居。
修复：起点 tenant + cluster fail-closed，接口加 capability gate。
"""
import pytest

from resource_graph import ResourceGraph, GraphNode, GraphEdge, TraversalDenied


TENANT = "t1"


def _graph():
    g = ResourceGraph()
    # cluster A 节点
    g.add_node(GraphNode("a-svc", "service", "cluster-A", TENANT))
    g.add_node(GraphNode("a-pod", "pod", "cluster-A", TENANT))
    g.add_edge(GraphEdge("a-svc", "a-pod", "depend"))
    # cluster B 节点
    g.add_node(GraphNode("b-svc", "service", "cluster-B", TENANT))
    g.add_node(GraphNode("b-pod", "pod", "cluster-B", TENANT))
    return g


def test_same_cluster_traversal_allowed():
    g = _graph()
    neighbors = g.neighbors("a-svc", cluster_id="cluster-A", tenant_id=TENANT)
    ids = [n.node_id for n in neighbors]
    assert "a-pod" in ids


def test_cross_cluster_pivot_denied():
    """审计 P0-5 复现：从 cluster-A 的节点，传入 cluster-B 的 scope → 拒绝。"""
    g = _graph()
    with pytest.raises(TraversalDenied):
        # 起点 a-svc 属于 cluster-A，但请求 scope cluster-B → 必须拒绝（fail-closed）
        g.neighbors("a-svc", cluster_id="cluster-B", tenant_id=TENANT)


def test_wrong_tenant_denied():
    g = _graph()
    with pytest.raises(TraversalDenied):
        g.neighbors("a-svc", cluster_id="cluster-A", tenant_id="other-tenant")


def test_capability_gate():
    """接口必须支持 capability 校验（fail-closed）。"""
    g = _graph()
    with pytest.raises(TraversalDenied):
        g.neighbors("a-svc", cluster_id="cluster-A", tenant_id=TENANT,
                    capability="observability.logs.read",
                    required_capability="observability.metrics.read")


def test_capability_match_allowed():
    g = _graph()
    neighbors = g.neighbors("a-svc", cluster_id="cluster-A", tenant_id=TENANT,
                            capability="observability.metrics.read",
                            required_capability="observability.metrics.read")
    assert any(n.node_id == "a-pod" for n in neighbors)
