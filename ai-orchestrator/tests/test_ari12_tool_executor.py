"""ARI.1/2 Agent 集成 InternalQueryClient + TrustedRequestContext — TDD 测试。

覆盖：
- T1 RealToolExecutor 经 InternalQueryClient 调用，URL 恒为 /internal/v1/query/*
- T2 TrustedRequestContext V2 携带 tenant/cluster/capability（+ request_id/timestamp/signature）
- T3 capability 精确（不自选，= Tool capability）
- T4 未注册 Tool 拒绝
- T5 operation 映射（tool_id → operation）
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from agent_runtime_integration import RealToolExecutor
from internal_query_client import InternalQueryClient
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


class FakeTransport:
    def __init__(self, status=200, body=b"{}"):
        self.calls = []
        self.status = status
        self.body = body

    def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
        self.calls.append({"path": path, "context_claims": dict(context_claims)})
        return self.status, self.body


@pytest.fixture
def transport():
    return FakeTransport()


@pytest.fixture
def executor(transport):
    issuer = TrustedContextIssuer(private_key=Ed25519PrivateKey.generate())
    client = InternalQueryClient(issuer=issuer, http=transport)
    return RealToolExecutor(client=client)


# ═══════════════════════════════════════════════════════
#  T1 URL 恒为 /internal/v1/query/*
# ═══════════════════════════════════════════════════════

class TestT1URL:
    def test_query_logs_url(self, executor, transport):
        transport.status, transport.body = 200, b'{"logs": [], "total": 0}'
        executor(params={"service": "checkout"}, tool_id="query_logs.v1", tenant_id=TENANT, cluster_id=CLUSTER, context={})
        assert transport.calls[0]["path"] == "/internal/v1/query/logs"

    def test_query_metrics_url(self, executor, transport):
        transport.status, transport.body = 200, b'{"points": [], "total": 0}'
        executor(params={"service": "checkout"}, tool_id="query_metrics.v1", tenant_id=TENANT, cluster_id=CLUSTER, context={})
        assert transport.calls[0]["path"] == "/internal/v1/query/metrics"


# ═══════════════════════════════════════════════════════
#  T2 TrustedRequestContext 携带 tenant/cluster/capability + 签名
# ═══════════════════════════════════════════════════════

class TestT2TrustedContext:
    def test_claims_include_scope_and_signature(self, executor, transport):
        transport.status, transport.body = 200, b"{}"
        executor(params={}, tool_id="query_logs.v1", tenant_id=TENANT, cluster_id=CLUSTER, context={})
        claims = transport.calls[0]["context_claims"]
        assert claims["tenant_id"] == TENANT
        assert claims["cluster_id"] == CLUSTER
        assert claims["capability"] == "observability.logs.read"
        assert claims["request_id"]  # 防重放
        assert claims["nonce"]  # 签名/唯一


# ═══════════════════════════════════════════════════════
#  T3 capability 精确
# ═══════════════════════════════════════════════════════

class TestT3CapabilityExact:
    def test_capability_from_tool(self, executor, transport):
        transport.status, transport.body = 200, b"{}"
        executor(params={}, tool_id="query_logs.v1", tenant_id=TENANT, cluster_id=CLUSTER, context={})
        # capability = query_logs.v1 的 capability，非调用方传入
        assert transport.calls[0]["context_claims"]["capability"] == "observability.logs.read"


# ═══════════════════════════════════════════════════════
#  T4 未注册 Tool 拒绝
# ═══════════════════════════════════════════════════════

class TestT4Unregistered:
    def test_unregistered_tool_rejected(self, executor):
        with pytest.raises(ValueError):
            executor(params={}, tool_id="evil_tool.v1", tenant_id=TENANT, cluster_id=CLUSTER, context={})


# ═══════════════════════════════════════════════════════
#  T5 operation 映射
# ═══════════════════════════════════════════════════════

class TestT5OperationMapping:
    def test_operation_mapping(self):
        assert RealToolExecutor._tool_operation("query_logs.v1") == "logs"
        assert RealToolExecutor._tool_operation("query_metrics.v1") == "metrics"
        assert RealToolExecutor._tool_operation("query_traces.v1") == "traces"
