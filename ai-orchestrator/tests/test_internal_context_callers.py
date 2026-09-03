import base64
import json
from datetime import datetime, timedelta, timezone
from uuid import UUID

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from contracts import RequestContext
from invocation_scope import LegacyScopeAdapter
from trusted_context import TrustedContextError


USER_ID = UUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
SESSION_ID = UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
TENANT_ID = UUID("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
CLUSTER_ID = UUID("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
RUN_ID = UUID("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")


def _context() -> LegacyScopeAdapter:
    now = datetime.now(timezone.utc)
    legacy = RequestContext(
        issuer="query-api",
        audience="ai-orchestrator",
        request_id=UUID("ffffffff-ffff-4fff-8fff-ffffffffffff"),
        run_id=RUN_ID,
        user_id=USER_ID,
        session_id=SESSION_ID,
        tenant_id=TENANT_ID,
        cluster_id=CLUSTER_ID,
        source="investigation",
        capability="observability.read",
        issued_at=now,
        expires_at=now + timedelta(seconds=30),
        nonce=UUID("11111111-1111-4111-8111-111111111111"),
    )
    # Old AI Chat path: legacy RequestContext wrapped as a ScopeView adapter.
    return LegacyScopeAdapter(legacy)


def _configure(monkeypatch) -> Ed25519PrivateKey:
    key = Ed25519PrivateKey.generate()
    raw = key.private_bytes_raw()
    encoded = base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")
    monkeypatch.setenv("QUERY_API_URL", "https://query-api.example/api/v1")
    monkeypatch.setenv("INTERNAL_TOKEN", "service-credential")
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", encoded)
    return key


class _Response:
    status = 200

    def __init__(self, body: bytes = b'{}'):
        self._body = body

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False

    def read(self, size=-1):
        return self._body if size is None or size < 0 else self._body[:size]


def _decode_claims(token: str) -> dict:
    payload = token.split(".")[1]
    payload += "=" * (-len(payload) % 4)
    return json.loads(base64.urlsafe_b64decode(payload))


def test_signed_query_api_request_sends_only_service_and_fresh_canonical_context(monkeypatch):
    """Dropping either trusted header, or delegating different identity/scope, must break this test."""
    _configure(monkeypatch)
    captured = []
    monkeypatch.setattr("urllib.request.urlopen", lambda request, timeout: captured.append((request, timeout)) or _Response(b'{"ok":true}'))

    from internal_query import signed_query_api_request

    body = signed_query_api_request(
        "https://query-api.example/api/v1/services?cluster_id=" + str(CLUSTER_ID),
        context=_context(),
        headers={"Accept": "application/json"},
    )

    assert body == b'{"ok":true}'
    request, timeout = captured[0]
    headers = {name.lower(): value for name, value in request.header_items()}
    assert headers["x-internal-token"] == "service-credential"
    assert headers["x-trusted-request-context"].count(".") == 2
    assert headers["accept"] == "application/json"
    for forbidden in (
        "x-internal-role", "x-internal-user", "x-internal-approver",
        "x-internal-scope", "x-tenant-id", "credential-ref", "authorization",
    ):
        assert forbidden not in headers

    claims = _decode_claims(headers["x-trusted-request-context"])
    # V9.2 TrustedRequestContext claims (P3.9-A): principal + scope, not user_id.
    assert claims["context_type"] == "trusted_request"
    assert claims["principal_type"] == "user"
    assert claims["principal_id"] == str(USER_ID)
    assert claims["session_id"] == str(SESSION_ID)
    assert claims["tenant_id"] == str(TENANT_ID)
    assert claims["scope_kind"] == "cluster"
    assert claims["cluster_id"] == str(CLUSTER_ID)
    assert claims["run_id"] == str(RUN_ID)
    assert claims["audience"] == "ai-apm-query-go"
    assert claims["issuer"] == "ai-orchestrator"
    assert 30 <= (
        datetime.fromisoformat(claims["expires_at"].replace("Z", "+00:00"))
        - datetime.fromisoformat(claims["issued_at"].replace("Z", "+00:00"))
    ).total_seconds() <= 60
    assert claims["request_id"] != str(_context().request_id)
    assert claims["nonce"] != str(_context().nonce)
    assert timeout == 10


def test_load_private_key_accepts_go_ed25519_64_byte_private_key(monkeypatch):
    """The Helm generator follows Go's seed||public 64-byte private-key contract."""
    key = Ed25519PrivateKey.generate()
    raw64 = key.private_bytes_raw() + key.public_key().public_bytes_raw()
    encoded = base64.urlsafe_b64encode(raw64).rstrip(b"=").decode("ascii")
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", encoded)

    from internal_query import _load_private_key

    loaded = _load_private_key(encoded)
    assert loaded.private_bytes_raw() == key.private_bytes_raw()


@pytest.mark.parametrize(
    ("missing", "error_code"),
    [
        ("context", "invalid_context"),
        ("service", "invalid_service"),
        ("signing_key", "invalid_signature"),
    ],
)
def test_signed_query_api_request_fails_before_network_when_trust_input_missing(monkeypatch, missing, error_code):
    _configure(monkeypatch)
    if missing == "service":
        monkeypatch.delenv("INTERNAL_TOKEN")
    if missing == "signing_key":
        monkeypatch.delenv("TRUSTED_CONTEXT_PRIVATE_KEY")
    monkeypatch.setattr("urllib.request.urlopen", lambda *_args, **_kwargs: pytest.fail("network must not be called"))

    from internal_query import signed_query_api_request

    with pytest.raises(TrustedContextError) as exc:
        signed_query_api_request(
            "https://query-api.example/api/v1/services",
            context=None if missing == "context" else _context(),
        )
    assert exc.value.error_code == error_code


def test_signed_query_api_request_rejects_legacy_authority_headers_and_external_urls(monkeypatch):
    _configure(monkeypatch)
    monkeypatch.setattr("urllib.request.urlopen", lambda *_args, **_kwargs: pytest.fail("network must not be called"))

    from internal_query import signed_query_api_request

    for headers in ({"X-Tenant-ID": "default"}, {"X-Internal-Role": "admin"}, {"Credential-Ref": "secret://cluster"}):
        with pytest.raises(TrustedContextError) as exc:
            signed_query_api_request(
                "https://query-api.example/api/v1/services",
                context=_context(),
                headers=headers,
            )
        assert exc.value.error_code == "invalid_context"

    with pytest.raises(TrustedContextError) as exc:
        signed_query_api_request("https://attacker.example/collect", context=_context())
    assert exc.value.error_code == "invalid_service"


def test_rca_query_requires_context_and_uses_signed_helper(monkeypatch):
    import rca

    calls = []
    monkeypatch.setattr(
        rca,
        "signed_query_api_request",
        lambda url, *, context, **kwargs: calls.append((url, context, kwargs)) or b'{"data":[]}',
    )

    assert rca._get_json("/services", request_context=_context()) == {"data": []}
    assert calls and calls[0][1].tenant_id == TENANT_ID
    assert "tenant" not in calls[0][0].lower()

    calls.clear()
    assert rca._get_json("/services", request_context=None).get("error") == "invalid_context"
    assert calls == []


def test_k8s_rca_does_not_fallback_to_orchestrator_shell(monkeypatch):
    import rca

    monkeypatch.setattr(
        rca,
        "cluster_check",
        lambda *_args, **_kwargs: pytest.fail(
            "canonical RCA must not read Kubernetes credentials through shell"
        ),
    )

    result = rca.full_rca_analysis(
        "kubernetes",
        anomaly_event={
            "rule_id": "k8s-pod-crash",
            "rule_name": "Pod 频繁重启",
            "message": "pod restarted",
        },
    )

    assert result["mode"] == "deterministic"
    assert "kubectl" in result["result"]["recommendation"]


def test_generic_probe_never_attaches_internal_authority(monkeypatch):
    from skills import diagnose

    captured = []
    monkeypatch.setattr(diagnose, "_is_blocked_host", lambda _host: False)
    monkeypatch.setattr(diagnose.urllib.request, "urlopen", lambda request, timeout: captured.append(request) or _Response(b"ok"))

    assert diagnose.probe_http("https://public.example/health").startswith("HTTP 200")
    headers = {name.lower(): value for name, value in captured[0].header_items()}
    assert "x-internal-token" not in headers
    assert "x-trusted-request-context" not in headers
    assert "x-tenant-id" not in headers


def test_investigator_writeback_reuses_explicit_context_for_read_and_write(monkeypatch):
    import investigator

    calls = []

    def fake_request(url, *, context, **kwargs):
        calls.append((url, context, kwargs))
        if len(calls) == 1:
            return b'{"data":[{"id":"alert-1","status":"firing"}]}'
        return b'{}'

    monkeypatch.setattr(investigator, "signed_query_api_request", fake_request, raising=False)

    result = investigator._writeback_to_alert_event(
        "high-cpu", "investigation", request_context=_context()
    )

    assert "alert-1" in result
    assert len(calls) == 2
    assert all(call[1].tenant_id == TENANT_ID for call in calls)
    assert calls[1][2]["method"] == "POST"
    assert calls[1][2]["headers"] == {"Content-Type": "application/json"}


def test_orchestrator_alert_collection_requires_explicit_context(monkeypatch):
    try:
        import orchestrator
    except ModuleNotFoundError as exc:
        if exc.name == "langgraph.store.base":
            pytest.skip("local LangGraph installation is incomplete")
        raise

    calls = []

    def fake_request(url, *, context, **kwargs):
        calls.append((url, context, kwargs))
        return b'{"data":[]}'

    monkeypatch.setattr(orchestrator, "signed_query_api_request", fake_request, raising=False)

    assert "活跃告警事件" in orchestrator._collect_alerts(request_context=_context())
    assert len(calls) == 2
    assert all(call[1].cluster_id == CLUSTER_ID for call in calls)

    calls.clear()
    assert "采集失败" in orchestrator._collect_alerts(request_context=None)
    assert calls == []


def test_alert_ops_uses_signed_helper_for_reads_and_writes(monkeypatch):
    from skills import alert_ops

    calls = []

    def fake_request(url, *, context, **kwargs):
        calls.append((url, context, kwargs))
        return b'{"data":[]}'

    monkeypatch.setattr(alert_ops, "signed_query_api_request", fake_request, raising=False)

    assert alert_ops._get_json(
        f"{alert_ops.QUERY_API}/alerts/rules", request_context=_context()
    ) == {"data": []}
    assert alert_ops._post(
        f"{alert_ops.QUERY_API}/alerts/events/1/ack",
        {"by": "ai-orchestrator"},
        request_context=_context(),
    ) == {"data": []}
    assert len(calls) == 2
    assert calls[1][2]["method"] == "POST"
    assert all(call[1].user_id == USER_ID for call in calls)


def test_observability_tools_reject_missing_context_and_use_signed_helper(monkeypatch):
    from tools import query_metrics

    assert query_metrics("orders", cluster_id=str(CLUSTER_ID)) == "查询失败: invalid_context"

    calls = []
    monkeypatch.setattr(
        "tools.signed_query_api_request",
        lambda url, *, context, **kwargs: calls.append((url, context, kwargs))
        or b'{"data": []}',
    )
    assert query_metrics(
        "orders", cluster_id=str(CLUSTER_ID), request_context=_context()
    ) == '{\n  "data": []\n}'
    assert calls and calls[0][1].cluster_id == CLUSTER_ID


def test_node_health_query_api_fallback_requires_explicit_context(monkeypatch):
    import node_health

    calls = []
    monkeypatch.setattr(node_health, "_VM_DIRECT_URL", "")
    monkeypatch.setattr(
        node_health,
        "signed_query_api_request",
        lambda url, *, context, **kwargs: calls.append((url, context, kwargs))
        or b'{"data":{"result":[]}}',
        raising=False,
    )

    assert node_health._vm_query_range(
        "up", 1, 2, request_context=_context()
    ) == []
    assert len(calls) == 1
    assert calls[0][1].session_id == SESSION_ID


class _WebSocketRequest:
    def __init__(self, query_params=None, headers=None):
        self.query_params = query_params or {}
        self.headers = headers or {}


def test_shell_is_manual_only_and_ignores_legacy_authority_claims(monkeypatch):
    import shell_ws

    monkeypatch.setenv("INTERNAL_TOKEN", "service-credential")
    monkeypatch.delenv("SHELL_MANUAL_ENABLED", raising=False)
    forged = _WebSocketRequest(
        query_params={
            "token": "service-credential",
            "role": "admin",
            "approver": "1",
            "user": "forged-user",
        },
        headers={"X-Internal-Role": "admin", "X-Internal-Approver": "1"},
    )
    assert shell_ws._is_ws_authorized(forged) is False

    monkeypatch.setenv("SHELL_MANUAL_ENABLED", "1")
    explicit_manual = _WebSocketRequest(
        query_params={}, headers={"X-Internal-Token": "service-credential"}
    )
    assert shell_ws._is_ws_authorized(explicit_manual) is True
