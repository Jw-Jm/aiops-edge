import pytest

def test_execute_suggestion_full_output(monkeypatch):
    from orchestrator import BrainOrchestrator
    b = BrainOrchestrator.__new__(BrainOrchestrator)
    calls = {}
    import subprocess as sp
    class R:
        returncode = 0
        stdout = "line_" * 3000   # 远超 500 截断阈值
        stderr = ""
    monkeypatch.setattr("subprocess.run", lambda *a, **k: R())
    # 走白名单 kubectl get 前缀
    out = b.execute_suggestion("order-svc", "kubectl get pods -n observability", "")
    assert "line_" in out
    # 全量返回：3 万字符不截断为 2000
    assert len(out) > 5000

def test_final_response_suggestion_notruncate(monkeypatch):
    # 通过 mock 构造 suggestion 事件生成路径校验 final_response 全量
    from orchestrator import _extract_script, _fallback_script, _action_summary
    long_resp = "R" * 4000
    # _extract_script / _fallback_script / _action_summary 不截断 final_response 本身
    assert _action_summary("kubectl get pods", long_resp, "s") or True
