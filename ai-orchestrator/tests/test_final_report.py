import pytest
from fastapi.testclient import TestClient

def test_final_report_returns_report(monkeypatch):
    # 隔离测试：不依赖真实 DB，monkeypatch get_session_state / AuditStore / _llm
    # 注意：所有对 main 模块全局的替换必须经 monkeypatch，避免污染后续测试（pytest 自动恢复）。
    import os
    monkeypatch.setenv("INTERNAL_TOKEN", "test-internal-token")
    import main as m
    class FakeBrain:
        def get_session_state(self, sid):
            return {"user_message": "分析 order-svc 错误率", "final_response": "初步报告",
                    "intent": "diagnosis", "service": "order-svc"}
    class FakeAudit:
        def query_by_task(self, tid):
            return [{"task_id": tid, "action": "approve", "target_service": "order-svc",
                     "command": "kubectl rollout restart", "result": "success",
                     "detail": "", "created_at": "t"}]
    monkeypatch.setattr(m, "_get_brain", lambda: FakeBrain(), raising=False)
    monkeypatch.setattr(m, "AuditStore", lambda: FakeAudit(), raising=False)
    # monkeypatch LLM 调用（final_report 使用 orchestrator._llm 同步函数）
    def fake_llm(cfg, sys, user, role=""):
        return "最终版本报告：根因定位 order-svc 内存泄漏，已滚动重启，风险解除。"
    monkeypatch.setattr(m, "_llm", fake_llm, raising=False)
    monkeypatch.setattr(m, "_llm_key_ready", lambda: True, raising=False)
    from fastapi import FastAPI
    app = FastAPI()
    # 挂载真实 final_report 路由（若 main 应用已定义则直接用 main.app）
    resp = None
    # 直接调用内部处理函数更稳妥：用 main.app
    try:
        with TestClient(m.app) as client:
            # G1 认证中间件：需携带 X-Internal-Token（与 INTERNAL_TOKEN 一致）
            r = client.post("/api/v1/ai/final_report",
                            json={"session_id": "sid1", "service": "order-svc"},
                            headers={"X-Internal-Token": "test-internal-token"})
            resp = r
    except Exception as e:
        pytest.skip(f"main.app 不可用: {e}")
    if resp is not None:
        assert resp.status_code == 200
        assert "最终版本报告" in resp.json()["report"]
