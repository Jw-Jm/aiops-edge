import asyncio
from datetime import datetime, timedelta, timezone
from uuid import UUID
import pytest

from contracts import RequestContext


def _context():
    now = datetime.now(timezone.utc)
    return RequestContext(
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
        monkeypatch.setattr("orchestrator.subprocess.run", fake_subprocess)
        # 注: node_collect 内的 query_logs/query_metrics 等经 tools 模块调用 tools._get_json,
        # 因此需 patch tools._get_json（orchestrator 未重导出 _get_json）。
        monkeypatch.setattr("tools._get_json", lambda *a, **k: {"data": [], "count": 0})
        # P1-6: K8sGPT 仅在 diagnosis 意图且非信息查询时调用（聊天/信息查询链路不再无条件调）
        state = {"service": "order-svc", "llm_config": None,
                 "intent": "diagnosis", "user_message": "分析 order-svc 错误率升高的原因",
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

    def unavailable(url, **_kwargs):
        if "/infrastructure/pods" in url:
            return {"pods": [], "error": "forbidden: admin or approver role required"}
        return {"nodes": [], "error": "forbidden: admin or approver role required"}

    monkeypatch.setattr("tools._get_json", unavailable)
    report = get_infrastructure(request_context=_context())

    assert "权限" in report or "forbidden" in report
    assert "数量未知" in report
    assert "运行中 Pods: 0 个" not in report
