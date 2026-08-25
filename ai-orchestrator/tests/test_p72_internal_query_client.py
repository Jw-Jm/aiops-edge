"""P7.2 InternalQueryClient + TrustedContextIssuer — TDD 测试（V9.3 Phase7）。

覆盖 P7.2 设计的 T1-T6 验收条件与安全边界：
- T1 正确签发与查询（合法 tenant/cluster/capability → 200；每次调用唯一 nonce/session/request_id）
- T2 禁旁路（URL 恒为 /internal/v1/query/*，无 direct DB/VM/VLogs 连接）
- T3 Scope 收敛（tenant/cluster/capability 隔离）
- T4 Replay 防护（复用 → CONTEXT_REPLAYED；过期 → 拒绝）
- T5 安全语义（permission_denied(403) ≠ no_data(200)；unavailable(503) 不降级；未注册 tool 拒绝）
- T6 Tool-Capability Binding Integrity（capability 仅来自 Tool Registry，LLM 不能注入）
"""
from __future__ import annotations

import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from uuid import UUID

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from tool_registry import ToolDefinition, ToolRegistry, init_default_tool_registry, KNOWN_CAPABILITIES
from trusted_context import (
    ReplayCache,
    TrustedContextError,
    VerifyConfig,
    sign_trusted_request_context_v2,
    verify_trusted_request_context_v2,
)


# ═══════════════════════════════════════════════════════
#  Helpers
# ═══════════════════════════════════════════════════════

def _now():
    return datetime.now(timezone.utc)


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def _kid(private_key) -> str:
    raw = private_key.public_key().public_bytes_raw()
    return _b64url(hashlib.sha256(raw).digest())


def _pubkey(private_key):
    return private_key.public_key()


def _verify_config(private_key) -> VerifyConfig:
    return VerifyConfig(
        issuer="ai-orchestrator",
        audience="ai-apm-query-go",
        public_keys={_kid(private_key): _pubkey(private_key)},
        replay_cache=ReplayCache(max_items=4096),
        clock_skew_seconds=30,
    )


def _reset_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    init_default_tool_registry()


@pytest.fixture(autouse=True)
def _fresh_registry():
    _reset_registry()
    yield
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()


@pytest.fixture
def issuer_key():
    return Ed25519PrivateKey.generate()


@pytest.fixture
def issuer(issuer_key):
    from trusted_context_issuer import TrustedContextIssuer

    return TrustedContextIssuer(private_key=issuer_key)


# 注入的假 http transport：记录调用，返回 (status, body_bytes)
class FakeTransport:
    def __init__(self, status=200, body=None):
        self.calls = []
        self.status = status
        self.body = (body or b"{}")

    def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
        self.calls.append(
            {
                "path": path,
                "context_claims": dict(context_claims),
                "method": method,
                "data": data,
                "headers": dict(headers or {}),
            }
        )
        return self.status, self.body


@pytest.fixture
def transport():
    return FakeTransport()


@pytest.fixture
def client(issuer, transport):
    from internal_query_client import InternalQueryClient

    return InternalQueryClient(issuer=issuer, http=transport)


# ═══════════════════════════════════════════════════════
#  T1 正确签发与查询
# ═══════════════════════════════════════════════════════

class TestT1SignAndQuery:
    def test_issuer_mints_verifiable_trusted_context(self, issuer, issuer_key):
        token = issuer.issue(
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            capability="observability.logs.read",
            run_id="22222222-2222-4222-8222-222222222222",
            principal_type="user",
            principal_id="33333333-3333-4333-8333-333333333333",
            session_id="44444444-4444-4444-8444-444444444444",
        )
        cfg = _verify_config(issuer_key)
        claims = verify_trusted_request_context_v2(token, cfg, _now())
        assert claims["context_type"] == "trusted_request"
        assert claims["tenant_id"] == "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
        assert claims["cluster_id"] == "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
        assert claims["capability"] == "observability.logs.read"

    def test_each_issue_has_unique_nonce_session_request(self, issuer):
        t1 = issuer.issue(
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            capability="observability.logs.read",
            run_id="22222222-2222-4222-8222-222222222222",
            principal_type="user",
            principal_id="33333333-3333-4333-8333-333333333333",
            session_id="44444444-4444-4444-8444-444444444444",
        )
        t2 = issuer.issue(
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            capability="observability.logs.read",
            run_id="22222222-2222-4222-8222-222222222222",
            principal_type="user",
            principal_id="33333333-3333-4333-8333-333333333333",
            session_id="55555555-5555-4555-8555-555555555555",
        )
        assert t1 != t2

        # 解出 claims 比较唯一性字段
        def _claims(token):
            payload = token.split(".")[1]
            pad = "=" * (-len(payload) % 4)
            return json.loads(base64.urlsafe_b64decode(payload + pad))

        c1, c2 = _claims(t1), _claims(t2)
        assert c1["nonce"] != c2["nonce"]
        assert c1["session_id"] != c2["session_id"]
        assert c1["request_id"] != c2["request_id"]
        # 短时效：issued_at/expires_at 各 30s 窗口
        issued = datetime.fromisoformat(c1["issued_at"].replace("Z", "+00:00"))
        expires = datetime.fromisoformat(c1["expires_at"].replace("Z", "+00:00"))
        assert expires - issued == timedelta(seconds=30)

    def test_query_logs_returns_200_and_forwards_context(self, client, transport):
        transport.status, transport.body = 200, b'{"logs": [{"ts": 1}], "total": 1}'
        res = client.query(
            tool_id="query_logs.v1",
            operation="logs",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            params={"service": "checkout", "minutes": 60},
            context_ref="run:22222222-2222-4222-8222-222222222222/tool:query_logs.v1",
        )
        assert res.http_status == 200
        assert res.body == {"logs": [{"ts": 1}], "total": 1}
        assert transport.calls[0]["path"] == "/internal/v1/query/logs"
        assert transport.calls[0]["method"] == "POST"
        assert transport.calls[0]["context_claims"]["capability"] == "observability.logs.read"
        assert transport.calls[0]["context_claims"]["tenant_id"] == "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
        assert transport.calls[0]["context_claims"]["cluster_id"] == "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
        assert transport.calls[0]["context_claims"]["workload_kind"] == "platform"


# ═══════════════════════════════════════════════════════
#  T2 禁旁路
# ═══════════════════════════════════════════════════════

class TestT2NoBypass:
    def test_every_operation_routes_only_to_internal_query(self, client, transport):
        transport.status, transport.body = 200, b"{}"
        cases = [
            ("metrics", "/internal/v1/query/metrics"),
            ("logs", "/internal/v1/query/logs"),
            ("traces", "/internal/v1/query/traces"),
            ("alerts", "/internal/v1/query/alerts"),
            ("topology", "/internal/v1/query/topology"),
            ("kubernetes", "/internal/v1/query/kubernetes"),
            ("changes", "/internal/v1/query/changes"),
            ("knowledge", "/internal/v1/query/knowledge"),
        ]
        tool_by_op = {
            "metrics": "query_metrics.v1",
            "logs": "query_logs.v1",
            "traces": "query_traces.v1",
            "alerts": "query_alerts.v1",
            "topology": "query_topology.v1",
            "kubernetes": "query_k8s.v1",
            "changes": "query_changes.v1",
            "knowledge": "knowledge_search.v1",
        }
        for op, expected_path in cases:
            client.query(
                tool_id=tool_by_op[op],
                operation=op,
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert len(transport.calls) == 8
        for call, (_, expected_path) in zip(transport.calls, cases):
            assert call["path"] == expected_path

    def test_unknown_operation_rejected(self, client):
        with pytest.raises(TrustedContextError) as exc:
            client.query(
                tool_id="query_logs.v1",
                operation="db_direct",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert exc.value.error_code == "invalid_context"

    def test_no_sql_or_promql_parameter_allowed(self, client):
        # 客户端禁止把 backend query language 传给内部端点（设计评审建议 2）
        with pytest.raises(TrustedContextError) as exc:
            client.query(
                tool_id="query_logs.v1",
                operation="logs",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={"sql": "select * from logs"},
                context_ref="c",
            )
        assert exc.value.error_code == "invalid_context"
        with pytest.raises(TrustedContextError):
            client.query(
                tool_id="query_metrics.v1",
                operation="metrics",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={"promql": 'sum(rate(foo[5m]))'},
                context_ref="c",
            )


# ═══════════════════════════════════════════════════════
#  T3 Scope 收敛
# ═══════════════════════════════════════════════════════

class TestT3ScopeConvergence:
    def test_tenant_id_flows_into_signed_context(self, client, transport):
        transport.status, transport.body = 200, b"{}"
        client.query(
            tool_id="query_logs.v1",
            operation="logs",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            params={},
            context_ref="c",
        )
        claims = transport.calls[0]["context_claims"]
        assert claims["tenant_id"] == "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
        assert claims["cluster_id"] == "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
        assert claims["scope_kind"] == "cluster"

    def test_capability_logs_read_cannot_reach_metrics_route(self, client):
        # query_logs.v1 的 capability 是 logs.read，不能签发 metrics.write / 调 metrics 端点
        with pytest.raises(TrustedContextError) as exc:
            client.query(
                tool_id="query_logs.v1",
                operation="metrics",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert exc.value.error_code == "invalid_context"


# ═══════════════════════════════════════════════════════
#  T4 Replay 防护
# ═══════════════════════════════════════════════════════

class TestT4ReplayProtection:
    def test_reusing_nonce_rejected(self, issuer, issuer_key):
        token = issuer.issue(
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            capability="observability.logs.read",
            run_id="22222222-2222-4222-8222-222222222222",
            principal_type="user",
            principal_id="33333333-3333-4333-8333-333333333333",
            session_id="44444444-4444-4444-8444-444444444444",
        )
        cfg = _verify_config(issuer_key)
        verify_trusted_request_context_v2(token, cfg, _now())
        with pytest.raises(TrustedContextError) as exc:
            verify_trusted_request_context_v2(token, cfg, _now())
        assert exc.value.error_code == "context_replayed"

    def test_expired_context_rejected(self, issuer, issuer_key):
        # 手动构造一个已过期的 context 供 verify 拒绝
        now = _now()
        claims = {
            "version": 1,
            "context_type": "trusted_request",
            "issuer": "ai-orchestrator",
            "audience": "ai-apm-query-go",
            "request_id": "11111111-1111-4111-8111-111111111111",
            "run_id": "22222222-2222-4222-8222-222222222222",
            "principal_type": "user",
            "principal_id": "33333333-3333-4333-8333-333333333333",
            "session_id": "44444444-4444-4444-8444-444444444444",
            "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            "scope_kind": "cluster",
            "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            "capability": "observability.logs.read",
            "source": "planner",
            "issued_at": now - timedelta(minutes=5),
            "expires_at": now - timedelta(minutes=4),
            "nonce": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        }
        token = sign_trusted_request_context_v2(claims, issuer_key)
        with pytest.raises(TrustedContextError) as exc:
            verify_trusted_request_context_v2(token, _verify_config(issuer_key), now)
        assert exc.value.error_code == "expired_context"


# ═══════════════════════════════════════════════════════
#  T5 安全语义（permission_denied ≠ no_data）
# ═══════════════════════════════════════════════════════

class TestT5SecuritySemantics:
    def test_403_permission_denied_not_no_data(self, client, transport):
        transport.status, transport.body = 403, b'{"error":"TENANT_ACCESS_DENIED","message":"unauthorized capability: observability.metrics.write"}'
        from internal_query_client import InternalQueryError

        with pytest.raises(InternalQueryError) as exc:
            client.query(
                tool_id="query_logs.v1",
                operation="logs",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert exc.value.kind == "permission_denied"
        assert exc.value.http_status == 403

    def test_503_unavailable_not_healthy(self, client, transport):
        transport.status, transport.body = 503, b'{"error":"BACKEND_UNAVAILABLE","message":"victoria metrics down"}'
        from internal_query_client import InternalQueryError

        with pytest.raises(InternalQueryError) as exc:
            client.query(
                tool_id="query_metrics.v1",
                operation="metrics",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={"service": "checkout"},
                context_ref="c",
            )
        assert exc.value.kind == "unavailable"
        assert exc.value.http_status == 503

    def test_200_with_no_data_is_legal_empty_result(self, client, transport):
        transport.status, transport.body = 200, b'{"error":"NO_DATA","message":"no data"}'
        res = client.query(
            tool_id="query_logs.v1",
            operation="logs",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            params={},
            context_ref="c",
        )
        assert res.http_status == 200
        assert res.body.get("error") == "NO_DATA"

    def test_unregistered_tool_rejected(self, client):
        from trusted_context import TrustedContextError

        with pytest.raises(TrustedContextError) as exc:
            client.query(
                tool_id="evil_tool.v1",
                operation="logs",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert exc.value.error_code == "invalid_context"


# ═══════════════════════════════════════════════════════
#  T6 Tool-Capability Binding Integrity
# ═══════════════════════════════════════════════════════

class TestT6CapabilityBinding:
    def test_query_metrics_tool_with_metrics_operation_allowed(self, client, transport):
        transport.status, transport.body = 200, b'{"points": [], "total": 0}'
        res = client.query(
            tool_id="query_metrics.v1",
            operation="metrics",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            params={"service": "checkout"},
            context_ref="c",
        )
        assert res.http_status == 200
        assert transport.calls[0]["context_claims"]["capability"] == "observability.metrics.read"

    def test_llm_cannot_inject_capability(self, client, transport):
        # capability 只能来自 Tool Registry；client 不接受调用方传 capability
        from trusted_context import TrustedContextError

        with pytest.raises(TypeError):
            client.query(
                tool_id="query_logs.v1",
                operation="logs",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
                capability="execution.shell",  # LLM 试图注入
            )

    def test_issuer_rejects_unknown_capability(self, issuer):
        with pytest.raises(TrustedContextError) as exc:
            issuer.issue(
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                capability="made_up.capability",
                run_id="22222222-2222-4222-8222-222222222222",
                principal_id="33333333-3333-4333-8333-333333333333",
            )
        assert exc.value.error_code == "invalid_context"

    def test_execute_tool_not_callable(self, client):
        # execute_k8s.v1 是 execution_state=disabled，不可作为查询 tool 走 internal query
        from trusted_context import TrustedContextError

        with pytest.raises(TrustedContextError) as exc:
            client.query(
                tool_id="execute_k8s.v1",
                operation="kubernetes",
                tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
                cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                params={},
                context_ref="c",
            )
        assert exc.value.error_code == "invalid_context"


# ═══════════════════════════════════════════════════════
#  生产默认 HTTP transport（_default_http）真实路径
#  —— 证明无旁路 + permission_denied(403) ≠ no_data(200) 在生产链路上成立
# ═══════════════════════════════════════════════════════

class TestDefaultHttpPath:
    """验证 _default_http 只发往 /internal/v1/query/*，且保留 HTTP 语义（不降级）。"""

    def _claims(self):
        return {
            "version": 1, "context_type": "trusted_request",
            "issuer": "ai-orchestrator", "audience": "ai-apm-query-go",
            "request_id": "11111111-1111-4111-8111-111111111111",
            "run_id": "22222222-2222-4222-8222-222222222222",
            "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
            "session_id": "44444444-4444-4444-8444-444444444444",
            "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            "scope_kind": "cluster", "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            "capability": "observability.logs.read", "source": "planner",
            "nonce": "55555555-5555-4555-8555-555555555555",
        }

    def _setup_env(self, monkeypatch, issuer_key):
        monkeypatch.setenv("QUERY_API_URL", "http://query-api.internal:8080")
        monkeypatch.setenv("INTERNAL_TOKEN", "svc-token")
        monkeypatch.setenv(
            "TRUSTED_CONTEXT_PRIVATE_KEY",
            base64.urlsafe_b64encode(issuer_key.private_bytes_raw()).decode("ascii"),
        )

    def _capture(self, monkeypatch, status=200):
        import urllib.request

        captured = {}

        class _FakeResp:
            def __init__(self, url):
                captured["url"] = url
                self.status = status

            def __enter__(self):
                return self

            def __exit__(self, *exc):
                return False

            def read(self):
                return b"{}"

        class _FakeReq:
            def __init__(self, url, data=None, method=None, headers=None):
                captured["url"] = url
                captured["headers"] = headers

        monkeypatch.setattr(urllib.request, "Request", _FakeReq)
        monkeypatch.setattr(urllib.request, "urlopen", lambda *a, **k: _FakeResp(captured["url"]))
        return captured

    def test_default_http_only_internal_query_url_with_signed_headers(self, monkeypatch, issuer_key):
        from internal_query_client import _default_http

        self._setup_env(monkeypatch, issuer_key)
        captured = self._capture(monkeypatch)
        status, _ = _default_http("/internal/v1/query/logs", context_claims=self._claims(), data=b"{}")
        # 唯一事实路径：只允许 QUERY_API_URL 下的 /internal/v1/query/*（禁旁路）
        assert captured["url"] == "http://query-api.internal:8080/internal/v1/query/logs"
        hdrs = captured["headers"]
        assert hdrs["X-Internal-Token"] == "svc-token"
        assert "X-Trusted-Request-Context" in hdrs  # EdDSA 签名 context 已携带
        assert status == 200

    def test_default_http_preserves_403_permission_denied(self, monkeypatch, issuer_key):
        import urllib.error
        import urllib.request

        from internal_query_client import _default_http

        self._setup_env(monkeypatch, issuer_key)

        class _FakeReq:
            def __init__(self, *a, **k):
                pass

        def _raise(*a, **k):
            raise urllib.error.HTTPError(url="x", code=403, msg="denied", hdrs={}, fp=None)

        monkeypatch.setattr(urllib.request, "Request", _FakeReq)
        monkeypatch.setattr(urllib.request, "urlopen", _raise)
        status, _ = _default_http("/internal/v1/query/logs", context_claims=self._claims())
        # 403 保留为 403，不被误判为 no_data(200) 或 healthy
        assert status == 403
