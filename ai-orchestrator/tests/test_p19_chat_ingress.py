"""P19.6: /internal/v1/chat trusted streaming ingress tests.

验证：
- 对话型 capability=ai.chat（独立于 ai.investigate）
- 验签通过 + ai.chat 授权 → SSE 流式返回
- 无 ai.chat capability → CAPABILITY_DENIED
- body tenant/cluster 与签名 scope 不匹配 → 拒绝
- 对话不创建 Investigation Run（不触发 ManualBoundary 建 Run）
- system principal / 重放 / 过期 → 401/409
"""

import base64
import hashlib
import json
import queue
import threading
import uuid
from datetime import datetime, timedelta, timezone

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

ISSUER = "query-api"
AUDIENCE = "ai-orchestrator"
PRINCIPAL = "33333333-3333-4333-8333-333333333333"
TENANT = "55555555-5555-4555-8555-555555555555"
CLUSTER = "66666666-6666-4666-8666-666666666666"


def _b64url(v):
    return base64.urlsafe_b64encode(v).rstrip(b"=").decode()


def _kid(pub):
    return _b64url(hashlib.sha256(pub.public_bytes_raw()).digest())


def _sign(claims, private_key):
    header = {"alg": "EdDSA", "kid": _kid(private_key.public_key()), "typ": "AIOPS-CONTEXT"}
    si = _b64url(json.dumps(header, sort_keys=True, separators=(",", ":")).encode()) + "." + \
         _b64url(json.dumps(claims, default=lambda o: o.astimezone(timezone.utc).isoformat().replace("+00:00", "Z"),
                            sort_keys=True, separators=(",", ":")).encode())
    sig = private_key.sign(si.encode())
    return si + "." + _b64url(sig)


def _keypair(seed=b"b1-test-chat-query-api-signer"):
    return Ed25519PrivateKey.from_private_bytes(hashlib.sha256(seed).digest())


def _claims(capability="ai.chat", principal_type="user", nonce=None,
            tenant=TENANT, cluster=CLUSTER):
    now = datetime.now(timezone.utc)
    claims = {
        "version": 1, "context_type": "run_invocation",
        "issuer": ISSUER, "audience": AUDIENCE,
        "request_id": str(uuid.uuid4()),
        "principal_type": principal_type, "principal_id": PRINCIPAL,
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": tenant, "source": "frontend",
        "cluster_scope": [cluster],
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": nonce or str(uuid.uuid4()),
    }
    if capability:
        claims["capability"] = capability
    return claims


def _configure(monkeypatch, private_key, token="svc-token"):
    pub = _b64url(private_key.public_key().public_bytes_raw())
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_VERIFY_KEYS", pub)
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_TOKEN", token)


def test_run_invocation_authz_error_does_not_echo_internal_details(monkeypatch):
    """授权失败只返回稳定码，不能泄漏矩阵/数据库/主体细节。"""
    import asyncio
    import apps.investigation as investigation
    from authorization_matrix import AuthzError

    monkeypatch.setattr(
        investigation,
        "verify_run_invocation_ingress",
        lambda _request: {
            "capability": "ai.investigate",
            "run_id": "run-1",
            "invocation_id": "inv-1",
            "principal_id": PRINCIPAL,
            "tenant_id": TENANT,
            "cluster_scope": [CLUSTER],
        },
    )

    class DenyMatrix:
        def authorize(self, **_kwargs):
            raise AuthzError("AUTHZ_DENIED", "mysql password=super-secret host=db.internal")

    monkeypatch.setattr(investigation, "_authz_matrix", DenyMatrix())

    class RequestStub:
        async def json(self):
            return {"run_id": "run-1", "invocation_id": "inv-1"}

    try:
        asyncio.run(investigation.run_invocation(RequestStub()))
    except Exception as exc:
        assert getattr(exc, "status_code", None) == 403
        detail = str(getattr(exc, "detail", ""))
        assert detail == "AUTHZ_DENIED"
        assert "super-secret" not in detail
        assert "db.internal" not in detail
    else:  # pragma: no cover - the endpoint must reject this request
        raise AssertionError("run invocation unexpectedly accepted denied authorization")


@pytest.fixture
def client(monkeypatch):
    from fastapi.testclient import TestClient
    import main

    class StubBrain:
        def __init__(self):
            self.calls = 0
            self.mode = "chat"
            self.request_ctx = None

        async def stream_sync(self, *args, **kwargs):
            self.calls += 1
            self.mode = kwargs.get("mode")
            self.request_ctx = kwargs.get("request_context")
            yield {"type": "progress", "text": "analyzing"}
            yield {"type": "done", "text": "ok"}

    stub = StubBrain()
    monkeypatch.setattr(main, "_get_brain", lambda: stub)

    # P13 接线：为对话测试 principal 注入 ai.chat 授权；另设一未授权 principal（无 ai.chat）。
    from authorization_matrix import AuthorizationMatrix, AuthzRule
    _mat = AuthorizationMatrix(service_account_roles={PRINCIPAL: "engineer"})
    _mat.add_rule(AuthzRule(principal=PRINCIPAL, tenant_id="*", cluster_id="*",
                            capability="ai.chat", action="create"))
    monkeypatch.setattr(main, "_authz_matrix", _mat)

    # 断言对话不触发 ManualBoundary 建 Run：若被调用则标记。
    _calls = {"manual_boundary": 0}
    original_boundary = main._manual_boundary
    class _TrackingBoundary:
        def require_user_explicit(self, **kwargs):
            _calls["manual_boundary"] += 1
    monkeypatch.setattr(main, "_manual_boundary", _TrackingBoundary())

    tc = TestClient(main.app)
    tc._stub_brain = stub  # type: ignore[attr-defined]
    tc._calls = _calls  # type: ignore[attr-defined]
    return tc


def _post_chat(client, private_key, claims, token="svc-token", body=None):
    jws = _sign(claims, private_key)
    headers = {"X-Internal-Token": token, "X-Trusted-Request-Context": jws}
    return client.post("/internal/v1/chat", headers=headers, json=body or {})


# ── positive: ai.chat → SSE stream ─────────────────────────────────────

def test_valid_ai_chat_returns_sse_stream(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post_chat(client, private_key, _claims(),
                      body={"message": "分析 order-svc", "service": "checkout",
                            "tenant_id": TENANT, "cluster_id": CLUSTER,
                            "turn_id": "77777777-7777-4777-8777-777777777777"})
    assert resp.status_code == 200, resp.text
    assert resp.headers["content-type"].startswith("text/event-stream")
    # SSE 帧包含 progress 与 done
    assert "event: progress" in resp.text
    assert "event: done" in resp.text
    assert "id: 1" in resp.text and "id: 2" in resp.text
    assert resp.headers["x-chat-turn-id"] == "77777777-7777-4777-8777-777777777777"
    assert client._stub_brain.calls == 1
    assert client._stub_brain.mode == "chat"
    # 对话不触发 ManualBoundary 建 Run（AI Chat 建 Run 边界）
    assert client._calls["manual_boundary"] == 0


# ── capability: ai.investigate 塞进 chat → 拒绝 ─────────────────────────

def test_chat_rejects_investigate_capability(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # capability=ai.investigate 是调查型，不应被 chat 入口接受
    resp = _post_chat(client, private_key, _claims(capability="ai.investigate"))
    assert resp.status_code == 403
    assert "CAPABILITY_DENIED" in resp.text
    assert client._stub_brain.calls == 0


# ── capability: 缺失 capability → fail-closed（不允许默认 ai.chat）──────

def test_chat_missing_capability_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # 上下文未显式携带 capability → 拒绝（不允许"默认 ai.chat"，防降级）。
    resp = _post_chat(client, private_key, _claims(capability=None))
    assert resp.status_code == 403
    assert "CAPABILITY_DENIED" in resp.text
    assert client._stub_brain.calls == 0


# ── 签名上下文字段完整性（用户 RBAC 权威 SoT 在 query-api，此处校验畸形上下文）──

def test_chat_missing_principal_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # 签名上下文缺 principal_id → orchestrator fail-closed（INVALID_CONTEXT）。
    import copy
    claims = copy.deepcopy(_claims())
    claims["principal_id"] = ""
    resp = _post_chat(client, private_key, claims)
    assert resp.status_code == 403
    assert "INVALID_CONTEXT" in resp.text
    assert client._stub_brain.calls == 0


# ── scope consistency ──────────────────────────────────────────────────

def test_chat_tenant_mismatch_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post_chat(client, private_key, _claims(),
                      body={"tenant_id": "99999999-9999-4999-8999-999999999999"})
    assert resp.status_code == 403
    assert client._stub_brain.calls == 0


def test_chat_cluster_mismatch_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post_chat(client, private_key, _claims(),
                      body={"cluster_id": "99999999-9999-4999-8999-999999999999"})
    assert resp.status_code == 403
    assert client._stub_brain.calls == 0


# ── system principal / replay / expired ────────────────────────────────

def test_chat_system_principal_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # system principal 无真实用户会话 → 由 query-api RequestAuthorizationContext 拒绝；
    # 在 orchestrator ingress，system 不应被当作对话用户放行（principal_type=system fail-closed）。
    resp = _post_chat(client, private_key, _claims(principal_type="system"))
    assert resp.status_code == 403
    assert client._stub_brain.calls == 0


def test_chat_nonce_replay_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # 用唯一 nonce，避免与其它测试共享模块级 replay cache 冲突。
    nonce = str(uuid.uuid4())
    claims = _claims(nonce=nonce)
    jws = _sign(claims, private_key)
    headers = {"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": jws}
    body = {"turn_id": "88888888-8888-4888-8888-888888888888"}
    resp1 = client.post("/internal/v1/chat", headers=headers, json=body)
    resp2 = client.post("/internal/v1/chat", headers=headers, json=body)
    assert resp1.status_code == 200
    assert resp2.status_code == 409


def test_chat_invalid_turn_id_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post_chat(client, private_key, _claims(), body={"turn_id": "not-a-uuid"})
    assert resp.status_code == 400
    assert "INVALID_TURN_ID" in resp.text
    assert client._stub_brain.calls == 0


def test_chat_expired_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    now = datetime.now(timezone.utc)
    claims = _claims()
    claims["issued_at"] = now - timedelta(seconds=120)
    claims["expires_at"] = now - timedelta(seconds=60)
    resp = _post_chat(client, private_key, claims)
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


def test_chat_stream_queue_is_bounded_and_honors_disconnect():
    import main

    assert main.CHAT_STREAM_QUEUE_MAXSIZE == 64
    event_queue = queue.Queue(maxsize=1)
    stop_event = threading.Event()
    assert main._put_chat_stream_event(event_queue, stop_event, {"type": "progress"})
    stop_event.set()
    assert not main._put_chat_stream_event(event_queue, stop_event, {"type": "done"})
    assert event_queue.qsize() == 1


def test_chat_stream_error_does_not_expose_internal_exception(monkeypatch, client):
    """Provider/SQL details must not cross the canonical SSE boundary."""
    private_key = _keypair()
    _configure(monkeypatch, private_key)

    async def failing_stream(*_args, **_kwargs):
        raise RuntimeError("provider api_key=super-secret host=10.0.0.7")
        yield  # keep this an async generator

    client._stub_brain.stream_sync = failing_stream
    resp = _post_chat(
        client,
        private_key,
        _claims(),
        body={"turn_id": "99999999-9999-4999-8999-999999999999"},
    )
    assert resp.status_code == 200
    assert "CHAT_BACKEND_ERROR" in resp.text
    assert "super-secret" not in resp.text
    assert "10.0.0.7" not in resp.text
