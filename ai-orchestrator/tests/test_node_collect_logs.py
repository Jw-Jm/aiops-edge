import asyncio
import subprocess
from datetime import datetime, timedelta, timezone
from uuid import UUID
import pytest
from cryptography.hazmat.primitives.serialization import (
    Encoding, PrivateFormat, NoEncryption)

from contracts import RequestContext
from invocation_scope import LegacyScopeAdapter


def _context() -> LegacyScopeAdapter:
    now = datetime.now(timezone.utc)
    legacy = RequestContext(
        issuer="ai-orchestrator", audience="ai-apm-query-go",
        request_id=UUID("11111111-1111-4111-8111-111111111111"),
        run_id=UUID("22222222-2222-4222-8222-222222222222"),
        user_id=UUID("33333333-3333-4333-8333-333333333333"),
        session_id=UUID("44444444-4444-4444-8444-444444444444"),
        tenant_id=UUID("55555555-5555-4555-8555-555555555555"),
        cluster_id=UUID("66666666-6666-4666-8666-666666666666"),
        source="test", capability="observability.read", issued_at=now,
        expires_at=now + timedelta(seconds=30),
        nonce=UUID("77777777-7777-4777-8777-777777777777"),
    )
    # Old AI Chat path: legacy RequestContext wrapped as a ScopeView adapter.
    return LegacyScopeAdapter(legacy)

def test_query_logs_returns_text(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url, **_kwargs):
        captured["url"] = url
        return {"data": [{"timestamp": "t", "service_name": "s", "severity": "ERROR", "body": "boom"}], "count": 1}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    out = query_logs("order-svc", cluster_id=str(_context().cluster_id), request_context=_context())
    assert "order-svc" in captured["url"] or "order-svc" in out
    assert "ERROR" in out or "boom" in out

def test_query_logs_empty_service_allows_all(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url, **_kwargs):
        captured["url"] = url
        return {"data": [], "count": 0}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    query_logs("", cluster_id=str(_context().cluster_id), request_context=_context())
    # 空 service 不追加 service_name 过滤，走全量
    assert "service=" not in captured["url"].split("?")[1]

def test_node_collect_includes_logs_and_k8sgpt(monkeypatch):
    from orchestrator import node_collect
    async def run():
        calls = {"k8sgpt": 0}
        real_run = __import__("subprocess").run
        def fake_subprocess(*a, **kw):
            argv = a[0] if a else kw.get("args", [])
            if isinstance(argv, (list, tuple)) and argv and "k8sgpt" in argv[0]:
                calls["k8sgpt"] += 1
                class R:
                    returncode = 0
                    stdout = "CRITICAL: pod X CrashLoopBackOff"
                    stderr = ""
                return R()
            return real_run(*a, **kw)
        monkeypatch.setattr(subprocess, "run", fake_subprocess)
        # node_collect 的 k8sgpt 按需分支先经 _fetch_llm_config_for_k8sgpt 校验 LLM provider 配置
        # （真实环境由运维注入）；测试环境隔离外部 LLM provider 依赖，返回最小配置以触发 k8sgpt 调用。
        monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt",
                            lambda: {"api_key": "test-key", "model": "gpt-test",
                                     "base_url": "https://api.openai.com/v1"})
        # 注: node_collect 内的 query_logs/query_metrics 等经 tools 模块调用 tools._get_json,
        # 因此需 patch tools._get_json（orchestrator 未重导出 _get_json）。
        monkeypatch.setattr("tools._get_json", lambda *a, **k: {"data": [], "count": 0})
        # P1-6: K8sGPT 仅在 diagnosis 意图且非信息查询时调用（聊天/信息查询链路不再无条件调）
        # 使用显式 k8sgpt 路由（user_message 含 "k8sgpt" 关键词）触发按需诊断，
        # 与真实环境「用户显式要求 k8sgpt 诊断」一致。
        state = {"service": "order-svc", "llm_config": None,
                 "intent": "diagnosis", "user_message": "用 k8sgpt 诊断 order-svc 错误率升高的根因",
                 "cluster_id": str(_context().cluster_id), "request_context": _context()}
        res = await node_collect(state)
        assert "logs_data" in res
        assert calls["k8sgpt"] >= 1
    asyncio.run(run())


def test_node_collect_skips_k8sgpt_for_info_query(monkeypatch):
    """P1-6: 信息查询/非 diagnosis 意图不调用 k8sgpt（降低对话延迟）。"""
    from orchestrator import node_collect
    async def run():
        calls = {"k8sgpt": 0}
        real_run = __import__("subprocess").run
        def fake_subprocess(*a, **kw):
            argv = a[0] if a else kw.get("args", [])
            if isinstance(argv, (list, tuple)) and argv and "k8sgpt" in argv[0]:
                calls["k8sgpt"] += 1
                class R:
                    returncode = 0
                    stdout = "CRITICAL: pod X CrashLoopBackOff"
                    stderr = ""
                return R()
            return real_run(*a, **kw)
        monkeypatch.setattr("orchestrator.subprocess.run", fake_subprocess)
        monkeypatch.setattr("tools._get_json", lambda *a, **k: {"data": [], "count": 0})
        # 信息查询意图（"有哪些服务在运行"）→ 跳过 K8sGPT
        state = {"service": "", "llm_config": None,
                 "intent": "chat", "user_message": "当前有哪些服务在运行?",
                 "cluster_id": str(_context().cluster_id), "request_context": _context()}
        await node_collect(state)
        assert calls["k8sgpt"] == 0, "信息查询/非 diagnosis 意图不应调用 k8sgpt"
    asyncio.run(run())


def test_infrastructure_permission_error_is_not_reported_as_zero_resources(monkeypatch):
    from tools import get_infrastructure
    import urllib.error

    # get_infrastructure（P19）已内联签名 + HTTP 调用，不再走 tools._get_json。
    # 提供最小合法签名凭据（TRUSTED_CONTEXT_PRIVATE_KEY）让签名阶段通过，
    # 再让内部 urlopen 返回 403 forbidden，模拟真实环境「权限不足」响应。
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    import hashlib, base64
    priv = Ed25519PrivateKey.from_private_bytes(
        hashlib.sha256(b"test-infra-key").digest())
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY",
                       base64.b64encode(priv.private_bytes(
                           Encoding.Raw, PrivateFormat.Raw, NoEncryption())).decode())
    monkeypatch.setenv("INTERNAL_TOKEN", "svc-token")
    monkeypatch.setenv("QUERY_API_URL", "http://query-api.svc:8080/api/v1")

    class _ForbiddenResp:
        status = 403
        def read(self): return b"forbidden: admin or approver role required"

    def _fake_urlopen(req, timeout=0):
        raise urllib.error.HTTPError(
            req.full_url, 403, "forbidden", {}, _ForbiddenResp())

    monkeypatch.setattr("urllib.request.urlopen", _fake_urlopen)
    report = get_infrastructure(request_context=_context())

    assert "权限" in report or "forbidden" in report
    assert "数量未知" in report
    assert "运行中 Pods: 0 个" not in report


def test_infrastructure_unwraps_query_tool_result_envelope(monkeypatch):
    """The canonical internal query response wraps K8s data under ``data``."""
    from tools import get_infrastructure
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    import base64, hashlib, json

    priv = Ed25519PrivateKey.from_private_bytes(hashlib.sha256(b"test-infra-envelope-key").digest())
    monkeypatch.setenv(
        "TRUSTED_CONTEXT_PRIVATE_KEY",
        base64.b64encode(priv.private_bytes(Encoding.Raw, PrivateFormat.Raw, NoEncryption())).decode(),
    )
    monkeypatch.setenv("INTERNAL_TOKEN", "svc-token")
    monkeypatch.setenv("QUERY_API_URL", "https://query-api.svc:8080/api/v1")

    class _Resp:
        status = 200
        def __enter__(self):
            return self
        def __exit__(self, *_):
            return False
        def read(self):
            return json.dumps({
                "quality": "complete", "count": 1, "digest": "abc",
                "data": {
                    "nodes": ["node-a"],
                    "node_details": [{"name": "node-a", "status": "Ready", "cpu": "4", "memory": "8Gi"}],
                    "pods": [{"name": "api", "namespace": "default", "status": "Running", "restarts": 0}],
                },
            }).encode()

    monkeypatch.setattr("tools.mtls_urlopen", lambda req, timeout=0: _Resp())
    report = get_infrastructure(request_context=_context())

    assert "运行中 Pods: 1 个" in report
    assert "节点: 1 个" in report
    assert "default/api: Running" in report
