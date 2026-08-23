"""ARI.3 Resource Graph query-api provider — TDD 测试。

覆盖：
- T1 经 query-api 采集（URL 为 /internal/v1/query/topology，非直连 informer）
- T2 采集构建 typed graph（node/edge）
- T3 隔离（跨 cluster 不泄漏）
- T4 无直连 informer/CMDB（provider 无 kubectl/direct_connect）
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from internal_query_client import InternalQueryClient
from resource_graph import GraphEdge, GraphNode
from resource_graph_provider import ResourceGraphProvider
from tool_registry import ToolRegistry, init_default_tool_registry
from trusted_context_issuer import TrustedContextIssuer


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
OTHER_CLUSTER = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"


class FakeTransport:
    def __init__(self, body=None):
        self.calls = []
        self.body = body or b'{"nodes": [], "edges": []}'

    def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
        self.calls.append({"path": path, "context_claims": dict(context_claims)})
        return 200, self.body


def _topology_body():
    return (
        '{"nodes": [{"id": "svc-checkout", "type": "service", "cluster": "91771a6e-9c2d-11f1-8271-bea176fe9f9f"},'
        ' {"id": "pod-checkout-0", "type": "pod", "cluster": "91771a6e-9c2d-11f1-8271-bea176fe9f9f"},'
        ' {"id": "svc-other", "type": "service", "cluster": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}],'
        ' "edges": [{"source": "svc-checkout", "target": "pod-checkout-0", "relation": "owns"},'
        ' {"source": "svc-checkout", "target": "svc-other", "relation": "cross"}]}'
    ).encode()


@pytest.fixture
def transport():
    return FakeTransport(body=_topology_body())


@pytest.fixture
def provider(transport):
    issuer = TrustedContextIssuer(private_key=Ed25519PrivateKey.generate())
    client = InternalQueryClient(issuer=issuer, http=transport)
    return ResourceGraphProvider(client=client)


# ═══════════════════════════════════════════════════════
#  T1 经 query-api 采集
# ═══════════════════════════════════════════════════════

class TestT1ViaQueryApi:
    def test_fetch_uses_topology_endpoint(self, provider, transport):
        provider.fetch_topology(tenant_id=TENANT, cluster_id=CLUSTER, context={})
        assert transport.calls[0]["path"] == "/internal/v1/query/topology"


# ═══════════════════════════════════════════════════════
#  T2 构建 typed graph
# ═══════════════════════════════════════════════════════

class TestT2BuildGraph:
    def test_nodes_and_edges(self, provider):
        graph = provider.fetch_topology(tenant_id=TENANT, cluster_id=CLUSTER, context={})
        assert graph.node("svc-checkout").type == "service"
        assert len(graph.edges()) == 2


# ═══════════════════════════════════════════════════════
#  T3 隔离（跨 cluster 不泄漏）
# ═══════════════════════════════════════════════════════

class TestT3Isolation:
    def test_cross_cluster_not_in_neighbors(self, provider):
        graph = provider.fetch_topology(tenant_id=TENANT, cluster_id=CLUSTER, context={})
        neighbors = graph.neighbors("svc-checkout", cluster_id=CLUSTER, tenant_id=TENANT)
        ids = [n.node_id for n in neighbors]
        assert "pod-checkout-0" in ids
        assert "svc-other" not in ids  # 跨 cluster 不泄漏


# ═══════════════════════════════════════════════════════
#  T4 无直连 informer/CMDB
# ═══════════════════════════════════════════════════════

class TestT4NoDirectSource:
    def test_no_direct_connector(self, provider):
        assert not hasattr(provider, "kubectl")
        assert not hasattr(provider, "informer")
        assert not hasattr(provider, "cmdb")
