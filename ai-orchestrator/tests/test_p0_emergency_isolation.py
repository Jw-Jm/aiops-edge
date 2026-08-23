"""P0 紧急隔离（安全整改）测试。

审计结论：main.py 旧入口存在未验签路径（_request_context_from_request 只解码不验签）、
cron 可后台自动创建并运行 Run（绕过 ManualBoundary）、审批后直接执行脚本等 P0 阻断项。
本文件为这些 fail-closed 行为提供回归保护。

边界：In-memory + TDD；不接真实 query-api/K8s。
"""
import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone

import pytest


def _b64url(v):
    return base64.urlsafe_b64encode(v).rstrip(b"=").decode()


def _unsigned_context_header(tenant_id="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
                             cluster_id="cccccccc-cccc-4ccc-8ccc-cccccccccccc"):
    """构造一个『仅 base64 编码、未签名』的伪造 JWS payload。

    旧 `_request_context_from_request` 只解码第 2 段不验签，此类伪造 context
    会被当作合法；审计 P0 要求该路径 fail-closed 拒绝。
    """
    now = datetime.now(timezone.utc)
    claims = {
        "version": 1, "context_type": "run_invocation",
        "issuer": "query-api", "audience": "ai-orchestrator",
        "request_id": "11111111-1111-4111-8111-111111111111",
        "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": tenant_id, "source": "frontend",
        "cluster_scope": [cluster_id],
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": "11111111-2222-4333-8444-555555555555",
    }
    header = {"alg": "EdDSA", "typ": "AIOPS-CONTEXT"}
    header_b64 = _b64url(json.dumps(header, sort_keys=True).encode())
    payload_b64 = _b64url(json.dumps(claims, default=str, sort_keys=True).encode())
    return f"{header_b64}.{payload_b64}."  # 无签名段


def _keypair(seed=b"b1-test-query-api-signer"):
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    return Ed25519PrivateKey.from_private_bytes(hashlib.sha256(seed).digest())


@pytest.fixture
def client(monkeypatch):
    from fastapi.testclient import TestClient
    import main

    class StubBrain:
        def __init__(self):
            self.executed = 0

        async def stream_sync(self, *args, **kwargs):
            yield {"type": "done", "text": "ok"}

        async def approve_and_resume(self, tid, approved=True):
            return {"final_response": "resumed"}

        def execute_suggestion(self, *args, **kwargs):
            self.executed += 1
            return "ok"

    stub = StubBrain()
    monkeypatch.setattr(main, "_get_brain", lambda: stub)
    # P13 接线：为 run-invocations 测试 principal 注入 ai.investigate 授权（否则 CAPABILITY_DENIED）
    from authorization_matrix import AuthorizationMatrix, AuthzRule
    _P = "33333333-3333-4333-8333-333333333333"
    _mat = AuthorizationMatrix(service_account_roles={_P: "engineer"})
    _mat.add_rule(AuthzRule(principal=_P, tenant_id="*", cluster_id="*",
                            capability="ai.investigate", action="create"))
    monkeypatch.setattr(main, "_authz_matrix", _mat)
    tc = TestClient(main.app)
    tc._stub_brain = stub  # type: ignore[attr-defined]
    return tc


def test_workflow_cron_disabled_by_default(monkeypatch):
    """默认 fail-closed：未显式配置时 workflow cron 不启用（禁后台自动创建 Run）。"""
    import main

    monkeypatch.delenv("ENABLE_WORKFLOW_CRON", raising=False)
    monkeypatch.delenv("RUN_CREATION_MODE", raising=False)
    assert main._workflow_cron_enabled() is False


def test_workflow_cron_requires_both_flags(monkeypatch):
    """仅设置 ENABLE_WORKFLOW_CRON 或仅 RUN_CREATION_MODE 都不足以启用。"""
    import main

    monkeypatch.setenv("ENABLE_WORKFLOW_CRON", "1")
    monkeypatch.delenv("RUN_CREATION_MODE", raising=False)
    assert main._workflow_cron_enabled() is False

    monkeypatch.setenv("RUN_CREATION_MODE", "auto")
    monkeypatch.delenv("ENABLE_WORKFLOW_CRON", raising=False)
    assert main._workflow_cron_enabled() is False


def test_workflow_cron_enabled_only_when_explicit_auto(monkeypatch):
    """仅当显式 ENABLE_WORKFLOW_CRON=1 且 RUN_CREATION_MODE=auto 才启用。"""
    import main

    monkeypatch.setenv("ENABLE_WORKFLOW_CRON", "1")
    monkeypatch.setenv("RUN_CREATION_MODE", "auto")
    assert main._workflow_cron_enabled() is True


def test_workflow_cron_not_auto_run_default(monkeypatch):
    """审计 P0：RUN_CREATION_MODE 默认为空，绝不默认 auto（禁后台自动 new Run）。"""
    import main

    monkeypatch.setenv("ENABLE_WORKFLOW_CRON", "1")
    monkeypatch.delenv("RUN_CREATION_MODE", raising=False)
    assert main._workflow_cron_enabled() is False


def _json_default(o):
    if isinstance(o, datetime):
        return o.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    raise TypeError(type(o))


def _sign_jws(claims, private_key):
    """构造完整 EdDSA JWS（签名）。datetime 用 ISO-8601 序列化，匹配服务端 _parse_datetime。"""
    header = {"alg": "EdDSA", "kid": _kid(private_key), "typ": "AIOPS-CONTEXT"}
    si = _b64url(json.dumps(header, sort_keys=True, separators=(",", ":")).encode()) + "." + \
         _b64url(json.dumps(claims, default=_json_default, sort_keys=True, separators=(",", ":")).encode())
    sig = private_key.sign(si.encode())
    return si + "." + _b64url(sig)


def _kid(private_key):
    return _b64url(hashlib.sha256(private_key.public_key().public_bytes_raw()).digest())


def _configure_verifier(monkeypatch, private_key, token="svc-token"):
    pub = _b64url(private_key.public_key().public_bytes_raw())
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_VERIFY_KEYS", pub)
    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_TOKEN", token)
    monkeypatch.setenv("INTERNAL_TOKEN", token)
    # P13 接线：为 run-invocations 测试 principal 注入 ai.investigate 授权（否则 CAPABILITY_DENIED）
    import main as _m
    from authorization_matrix import AuthorizationMatrix as _AM, AuthzRule as _AR
    _P = "33333333-3333-4333-8333-333333333333"
    _mat = _AM(service_account_roles={_P: "engineer"})
    _mat.add_rule(_AR(principal=_P, tenant_id="*", cluster_id="*",
                      capability="ai.investigate", action="create"))
    monkeypatch.setattr(_m, "_authz_matrix", _mat)


def test_legacy_entry_rejects_unsigned_context(monkeypatch, client):
    """审计 P0：旧入口不再『解码即信任』——仅 base64 编码的伪造 context 必须被拒绝。"""
    private_key = _keypair()
    _configure_verifier(monkeypatch, private_key)
    headers = {"X-Internal-Token": "svc-token",
               "X-Trusted-Request-Context": _unsigned_context_header()}
    resp = client.post("/api/v1/ops/rca", headers=headers, json={"service": "checkout"})
    # 未验签 → fail-closed 401/403，而非放行
    assert resp.status_code in (401, 403), resp.text


def test_legacy_entry_rejects_missing_service_token(monkeypatch, client):
    """审计 P0：缺 X-Internal-Token 时旧入口必须拒绝（不可仅靠伪造 context 通过）。"""
    private_key = _keypair()
    _configure_verifier(monkeypatch, private_key)
    headers = {"X-Trusted-Request-Context": _unsigned_context_header()}
    resp = client.post("/api/v1/ops/rca", headers=headers, json={"service": "checkout"})
    assert resp.status_code in (401, 403), resp.text


def test_new_verified_ingress_still_works(monkeypatch):
    """正向基线：唯一验签入口 /internal/v1/run-invocations 不被破坏（审计未要求关闭它）。"""
    from datetime import timezone
    import main
    from fastapi.testclient import TestClient
    private_key = _keypair()
    _configure_verifier(monkeypatch, private_key)
    now = datetime.now(timezone.utc)
    import uuid
    claims = {
        "version": 1, "context_type": "run_invocation",
        "issuer": "query-api", "audience": "ai-orchestrator",
        "request_id": "11111111-1111-4111-8111-111111111111",
        "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "source": "frontend",
        "cluster_scope": ["cccccccc-cccc-4ccc-8ccc-cccccccccccc"],
        "issued_at": now, "expires_at": now + timedelta(seconds=30),
        "nonce": str(uuid.uuid4()),
    }
    headers = {"X-Internal-Token": "svc-token",
               "X-Trusted-Request-Context": _sign_jws(claims, private_key)}
    resp = TestClient(main.app).post("/internal/v1/run-invocations",
                                     headers=headers, json={"intent": "investigate"})
    # 验签通过进入业务（返回 200/422 业务态，而非 401/403 鉴权拒绝）
    assert resp.status_code not in (401, 403), resp.text


def _seed_task(monkeypatch, client):
    """向 _task_store 植入一个 ai_chat 审批任务。"""
    import main
    tid = "test-approve-tid"
    main._task_store[tid] = {
        "id": tid, "status": "queued", "source": "ai_chat",
        "service": "checkout", "context": "fix error", "diagnosis": "",
        "plan": "", "script": "echo hi", "risk_score": 1, "risk_reason": "",
        "report": "", "created_at": "2026-01-01T00:00:00Z", "done_at": "",
    }
    monkeypatch.setenv("INTERNAL_TOKEN", "svc-token")
    return tid


def test_approval_does_not_execute_script_by_default(monkeypatch, client):
    """审计 P0：默认（EXECUTION_AFTER_APPROVAL 未启用）审批只记录状态、不执行脚本。"""
    tid = _seed_task(monkeypatch, client)
    headers = {"X-Internal-Token": "svc-token",
               "X-Internal-Role": "admin"}
    resp = client.post(f"/api/v1/ops/tasks/{tid}/approve", headers=headers, json={})
    assert resp.status_code == 200, resp.text
    body = resp.json()
    # 审批已记录但不触发真实执行
    assert body["task"]["status"] == "approved_pending_execution"
    assert "执行暂停" in body["note"]


def test_approval_executes_when_explicitly_enabled(monkeypatch, client):
    """审计 P0：仅显式 EXECUTION_AFTER_APPROVAL=1 才恢复审批后执行。"""
    tid = _seed_task(monkeypatch, client)
    monkeypatch.setenv("EXECUTION_AFTER_APPROVAL", "1")

    headers = {"X-Internal-Token": "svc-token",
               "X-Internal-Role": "admin"}
    resp = client.post(f"/api/v1/ops/tasks/{tid}/approve", headers=headers, json={})
    assert resp.status_code == 200, resp.text
    assert client._stub_brain.executed == 1  # 显式启用后才执行
