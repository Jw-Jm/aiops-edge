import asyncio
import pytest

def test_query_logs_returns_text(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url):
        captured["url"] = url
        return {"data": [{"timestamp": "t", "service_name": "s", "severity": "ERROR", "body": "boom"}], "count": 1}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    out = query_logs("order-svc")
    assert "order-svc" in captured["url"] or "order-svc" in out
    assert "ERROR" in out or "boom" in out

def test_query_logs_empty_service_allows_all(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url):
        captured["url"] = url
        return {"data": [], "count": 0}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    query_logs("")
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
                 "intent": "diagnosis", "user_message": "分析 order-svc 错误率升高的原因"}
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
                 "intent": "chat", "user_message": "当前有哪些服务在运行?"}
        await node_collect(state)
        assert calls["k8sgpt"] == 0, "信息查询/非 diagnosis 意图不应调用 k8sgpt"
    asyncio.run(run())
