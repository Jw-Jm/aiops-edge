"""P3.9-B1: /internal/v1/run-invocations trusted ingress tests."""

import base64
import hashlib
import json
import os
from datetime import datetime, timedelta, timezone

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from trusted_context import TrustedContextError

ISSUER = "query-api"
AUDIENCE = "ai-orchestrator"


def _b64url(v):
    return base64.urlsafe_b64encode(v).rstrip(b"=").decode()


def _json_default(o):
    if isinstance(o, datetime):
        return o.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    raise TypeError(type(o))


def _kid(pub):
    return _b64url(hashlib.sha256(pub.public_bytes_raw()).digest())


def _sign_run_invocation(claims, private_key):
    header = {"alg": "EdDSA", "kid": _kid(private_key.public_key()), "typ": "AIOPS-CONTEXT"}
    si = _b64url(json.dumps(header, sort_keys=True, separators=(",", ":")).encode()) + "." + \
         _b64url(json.dumps(claims, default=_json_default, sort_keys=True, separators=(",", ":")).encode())
    sig = private_key.sign(si.encode())
    return si + "." + _b64url(sig)


def _run_invocation_claims(tenant_id="55555555-5555-4555-8555-555555555555",
                           cluster="66666666-6666-4666-8666-666666666666", nonce=None):
    import uuid

    now = datetime.now(timezone.utc)
    return {
        "version": 1, "context_type": "run_invocation",
        "issuer": ISSUER, "audience": AUDIENCE,
        "request_id": "11111111-1111-4111-8111-111111111111",
        "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": tenant_id,
        "source": "frontend",
        "cluster_scope": [cluster],
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": nonce or str(uuid.uuid4()),
    }


def _run_control_claims(operation="cancel", nonce=None):
    import uuid

    now = datetime.now(timezone.utc)
    return {
        "version": 1, "context_type": "run_control",
        "issuer": ISSUER, "audience": AUDIENCE,
        "request_id": "11111111-1111-4111-8111-111111111111",
        "run_id": "22222222-2222-4222-8222-222222222222",
        "operation": operation,
        "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": "55555555-5555-4555-8555-555555555555",
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": nonce or str(uuid.uuid4()),
    }


def _post_control(client, operation, claims, private_key):
    jws = _sign_run_invocation(claims, private_key)
    headers = {"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": jws}
    return client.post(f"/internal/v1/run-controls/{operation}", headers=headers, json={})


def _keypair(seed=b"b1-test-query-api-signer"):
    private_key = Ed25519PrivateKey.from_private_bytes(hashlib.sha256(seed).digest())
    return private_key


def _configure(monkeypatch, private_key, token="svc-token"):
    pub = _b64url(private_key.public_key().public_bytes_raw())
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_VERIFY_KEYS", pub)
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_TOKEN", token)


@pytest.fixture
def client(monkeypatch):
    from fastapi.testclient import TestClient
    import main

    # Stub the AI Chat downstream: stream_sync returns no events.
    class StubBrain:
        def __init__(self):
            self.calls = 0

        async def stream_sync(self, *args, **kwargs):
            self.calls += 1
            yield {"type": "done", "text": "ok"}

    stub = StubBrain()
    monkeypatch.setattr(main, "_get_brain", lambda: stub)
    # P13 接线：为 run-invocations 测试 principal 注入 ai.investigate 授权（否则 CAPABILITY_DENIED）
    from authorization_matrix import AuthorizationMatrix, AuthzRule
    _PRINCIPAL = "33333333-3333-4333-8333-333333333333"
    _mat = AuthorizationMatrix(service_account_roles={_PRINCIPAL: "engineer"})
    _mat.add_rule(AuthzRule(principal=_PRINCIPAL, tenant_id="*", cluster_id="*",
                            capability="ai.investigate", action="create"))
    monkeypatch.setattr(main, "_authz_matrix", _mat)
    tc = TestClient(main.app)
    tc._stub_brain = stub  # type: ignore[attr-defined]
    return tc


def _post(client, token, headers_extra=None, body=None):
    private_key = _keypair()
    claims = _run_invocation_claims()
    jws = _sign_run_invocation(claims, private_key)
    headers = {"X-Internal-Token": token, "X-Trusted-Request-Context": jws}
    if headers_extra:
        headers.update(headers_extra)
    return client.post("/internal/v1/run-invocations", headers=headers, json=body or {})


# ── positive ──────────────────────────────────────────────────────────

def test_valid_run_invocation_accepted_and_downstream_called_once(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "svc-token")
    assert resp.status_code == 200, resp.text
    assert resp.json()["run_id"] == "11111111-1111-4111-8111-111111111111"
    assert client._stub_brain.calls == 1


def test_system_run_invocation_empty_session_sentinel_is_normalized(monkeypatch):
    """The Go dispatcher uses an empty string for a system principal's null session."""
    from internal_ingress import verify_run_invocation_ingress

    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims.update({
        "principal_type": "system",
        "principal_id": "f4a4b8c2-3d5e-4f6a-8b9c-0d1e2f3a4b5c",
        "session_id": "",
        "capability": "ai.investigate",
        "run_id": "22222222-2222-4222-8222-222222222222",
        "invocation_id": "99999999-9999-4999-8999-999999999999",
    })
    jws = _sign_run_invocation(claims, private_key)
    from starlette.requests import Request
    request = Request({
        "type": "http", "method": "POST", "path": "/internal/v1/run-invocations",
        "headers": [
            (b"x-internal-token", b"svc-token"),
            (b"x-trusted-request-context", jws.encode()),
        ],
        "query_string": b"", "scheme": "http", "server": ("test", 80),
        "client": ("127.0.0.1", 1), "root_path": "",
    })
    verified = verify_run_invocation_ingress(request)
    assert verified["principal_type"] == "system"
    assert verified["session_id"] is None
    assert verified["capability"] == "ai.investigate"


# ── service credential ─────────────────────────────────────────────────

def test_missing_service_credential_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "")  # missing token → verify returns empty service_token check
    # missing token header yields 401 from service token check
    assert resp.status_code in (401, 422)


def test_wrong_service_credential_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "wrong-token")
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


# ── unsigned / tampered / bad alg/typ / wrong key ──────────────────────

def test_unsigned_context_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": "not.a.jws"})
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


def test_tampered_context_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    jws = _sign_run_invocation(claims, private_key)
    parts = jws.split(".")
    tampered = parts[0] + "." + parts[1][:-1] + "A" + "." + parts[2]
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": tampered})
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


def test_wrong_key_rejected(monkeypatch, client):
    # configure with a different public key than the signer
    private_key = _keypair()
    _configure(monkeypatch, _keypair(b"different-seed"))
    resp = _post(client, "svc-token")
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


def test_wrong_context_type_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims["context_type"] = "trusted_request"  # wrong type for this endpoint
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 401
    assert client._stub_brain.calls == 0


def test_wrong_issuer_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims["issuer"] = "someone-else"
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 401


def test_wrong_audience_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims["audience"] = "other-service"
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 401


def test_expired_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    now = datetime.now(timezone.utc)
    claims = _run_invocation_claims()
    claims["issued_at"] = now - timedelta(seconds=120)
    claims["expires_at"] = now - timedelta(seconds=60)
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 401


def test_future_iat_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    now = datetime.now(timezone.utc)
    claims = _run_invocation_claims()
    claims["issued_at"] = now + timedelta(seconds=120)
    claims["expires_at"] = now + timedelta(seconds=150)
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 401


def test_nonce_replay_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # Same signed token (same nonce) used twice → second must be replayed.
    claims = _run_invocation_claims(nonce="11111111-1111-4111-8111-111111111111")
    jws = _sign_run_invocation(claims, private_key)
    headers = {"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": jws}
    resp1 = client.post("/internal/v1/run-invocations", headers=headers, json={})
    resp2 = client.post("/internal/v1/run-invocations", headers=headers, json={})
    assert resp1.status_code == 200
    assert resp2.status_code == 409


# ── scope consistency / multi-cluster ──────────────────────────────────

def test_tenant_mismatch_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "svc-token", body={"tenant_id": "99999999-9999-4999-8999-999999999999"})
    assert resp.status_code == 403


def test_cluster_mismatch_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    resp = _post(client, "svc-token", body={"cluster_id": "99999999-9999-4999-8999-999999999999"})
    assert resp.status_code == 403


def test_signed_and_body_run_id_must_match(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims.update({
        "capability": "ai.investigate",
        "run_id": "22222222-2222-4222-8222-222222222222",
        "invocation_id": "99999999-9999-4999-8999-999999999999",
    })
    jws = _sign_run_invocation(claims, private_key)
    resp = client.post(
        "/internal/v1/run-invocations",
        headers={"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": jws},
        json={"run_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
    )
    assert resp.status_code == 403
    assert resp.json()["detail"] == "RUN_ID_MISMATCH"
    assert client._stub_brain.calls == 0


def test_multi_cluster_run_invocation_refused(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()
    claims["cluster_scope"] = [
        "66666666-6666-4666-8666-666666666666",
        "88888888-8888-4888-8888-888888888888",
    ]
    jws = _sign_run_invocation(claims, private_key)
    resp = _post(client, "svc-token", headers_extra={"X-Trusted-Request-Context": jws})
    assert resp.status_code == 422  # VALIDATION_FAILED
    assert client._stub_brain.calls == 0


# ── RunControl ingress (P3.9-C1) ───────────────────────────────────────

def test_run_control_cancel_operation_binding(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_control_claims(operation="cancel")
    resp = _post_control(client, "cancel", claims, private_key)
    assert resp.status_code == 200
    assert resp.json()["operation"] == "cancel"


def test_run_control_wrong_operation_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    # signed operation=cancel sent to the stream endpoint → must reject.
    claims = _run_control_claims(operation="cancel")
    resp = _post_control(client, "stream", claims, private_key)
    assert resp.status_code == 403  # ACTION_NOT_ALLOWED


def test_run_control_non_control_context_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_invocation_claims()  # run_invocation sent to run-controls
    resp = _post_control(client, "cancel", claims, private_key)
    assert resp.status_code == 401


def test_run_control_bad_signature_rejected(monkeypatch, client):
    private_key = _keypair()
    _configure(monkeypatch, private_key)
    claims = _run_control_claims(operation="cancel")
    jws = _sign_run_invocation(claims, private_key)
    parts = jws.split(".")
    tampered = parts[0] + "." + parts[1][:-1] + "A" + "." + parts[2]
    headers = {"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": tampered}
    resp = client.post("/internal/v1/run-controls/cancel", headers=headers, json={})
    assert resp.status_code == 401
