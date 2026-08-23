"""P13 真实接线 — ManualBoundary + AuthorizationMatrix 接入唯一 Run 创建入口。

依据设计 docs/V9.3_P13_REAL_WIRING_DESIGN.md v0.1（审计 P0-5/P0-6）。
边界：In-memory + TDD；不接真实 query-api 权威角色 SoT。
"""
import base64
import hashlib
import json
import uuid
from datetime import datetime, timedelta, timezone

RUN = "11111111-1111-4111-8111-111111111111"
USER_PRINCIPAL = "33333333-3333-4333-8333-333333333333"
SYSTEM_PRINCIPAL = "33333333-3333-4333-8333-000000000000"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"


def _b64url(v):
    return base64.urlsafe_b64encode(v).rstrip(b"=").decode()


def _json_default(o):
    if isinstance(o, datetime):
        return o.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    raise TypeError(type(o))


def _keypair(seed=b"b1-test-query-api-signer"):
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    return Ed25519PrivateKey.from_private_bytes(hashlib.sha256(seed).digest())


def _sign_jws(claims, key):
    pub = _b64url(key.public_key().public_bytes_raw())
    kid = _b64url(hashlib.sha256(key.public_key().public_bytes_raw()).digest())
    header = {"alg": "EdDSA", "kid": kid, "typ": "AIOPS-CONTEXT"}
    si = _b64url(json.dumps(header, sort_keys=True, separators=(",", ":")).encode()) + "." + \
         _b64url(json.dumps(claims, default=_json_default, sort_keys=True, separators=(",", ":")).encode())
    sig = key.sign(si.encode())
    return si + "." + _b64url(sig), pub


def _claims(principal_type="user", principal_id=USER_PRINCIPAL):
    now = datetime.now(timezone.utc)
    return {
        "version": 1, "context_type": "run_invocation",
        "issuer": "query-api", "audience": "ai-orchestrator",
        "request_id": str(uuid.uuid4()),
        "principal_type": principal_type, "principal_id": principal_id,
        "session_id": str(uuid.uuid4()),
        "tenant_id": TENANT, "source": "frontend",
        "cluster_scope": [CLUSTER],
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": str(uuid.uuid4()),
    }


def _client(monkeypatch, service_account_roles=None):
    import main
    from authorization_matrix import AuthzRule
    from fastapi.testclient import TestClient
    key = _keypair()
    pub = _b64url(key.public_key().public_bytes_raw())
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_VERIFY_KEYS", pub)
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_TOKEN", "svc-token")
    monkeypatch.setenv("INTERNAL_TOKEN", "svc-token")
    monkeypatch.setenv("AIOPS_DATA_DIR", "/tmp/aiops-test")

    class StubBrain:
        async def stream_sync(self, *args, **kwargs):
            yield {"type": "done", "text": "ok"}

    monkeypatch.setattr(main, "_get_brain", lambda: StubBrain())
    # P13 接线：直接注入干净的角色映射 + ai.investigate 规则（避免依赖 import 时 env 时序）
    from authorization_matrix import AuthorizationMatrix
    roles = dict(service_account_roles or {})
    matrix = AuthorizationMatrix(service_account_roles=roles)
    for p in roles:
        matrix.add_rule(AuthzRule(principal=p, tenant_id="*", cluster_id="*",
                                  capability="ai.investigate", action="create"))
    monkeypatch.setattr(main, "_authz_matrix", matrix)
    tc = TestClient(main.app)
    return tc, key


def _post_run(client, key, claims, body=None):
    jws, _ = _sign_jws(claims, key)
    headers = {"X-Internal-Token": "svc-token", "X-Trusted-Request-Context": jws}
    return client.post("/internal/v1/run-invocations", headers=headers,
                       json=body or {"intent": "diagnose", "service": "checkout"})


def test_system_principal_rejected_for_run_creation(monkeypatch):
    """审计 P0-5：System Principal 不能创建 Run（ManualBoundary fail-closed）。"""
    client, key = _client(monkeypatch)
    resp = _post_run(client, key, _claims(principal_type="system", principal_id=SYSTEM_PRINCIPAL))
    assert resp.status_code == 403, resp.text


def test_user_principal_allowed_for_run_creation(monkeypatch):
    """审计 P0-5：已验证 user（配置为 engineer/有 ai.investigate）可创建 Run。"""
    client, key = _client(monkeypatch, service_account_roles={USER_PRINCIPAL: "engineer"})
    resp = _post_run(client, key, _claims(principal_type="user"))
    # 放行进入业务（非 403 MANUAL_TRIGGER_REQUIRED / CAPABILITY_DENIED）
    assert resp.status_code in (200, 422, 500), resp.text


def test_unauthorized_user_denied_for_run_creation(monkeypatch):
    """审计 P0-6：未配置角色的 user（viewer，无 ai.investigate）→ CAPABILITY_DENIED fail-closed。"""
    client, key = _client(monkeypatch, service_account_roles={})  # 空映射 → 所有 user viewer
    resp = _post_run(client, key, _claims(principal_type="user"))
    assert resp.status_code == 403, resp.text
    assert "CAPABILITY_DENIED" in resp.text or "AUTHZ_DENIED" in resp.text


def test_viewer_role_cannot_create_run(monkeypatch):
    """审计 P0-6：显式配置为 viewer 的 user → 无 ai.investigate → 拒绝。"""
    client, key = _client(monkeypatch, service_account_roles={USER_PRINCIPAL: "viewer"})
    resp = _post_run(client, key, _claims(principal_type="user"))
    assert resp.status_code == 403, resp.text
    assert "CAPABILITY_DENIED" in resp.text
