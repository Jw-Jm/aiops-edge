"""后台 worker + chat SSE task_notification 通知单元测试（B4）"""
import time

from agent_tool import (set_personas, set_on_done, spawn_worker,
                        drain_notifications, _notify)
from persona_registry import Persona


def _persona():
    return Persona(name="specialist-sre", when_to_use="w",
                   system_prompt="你是资深 SRE。", tools=[], max_turns=5)


def _wait_done(done, tries=100):
    for _ in range(tries):
        if done:
            return True
        time.sleep(0.02)
    return False


def test_background_not_blocking_and_on_done(monkeypatch):
    """background=True 不阻塞；完成回调被调用且携带 worker 名。"""
    set_personas({"specialist-sre": _persona()})
    monkeypatch.setattr("agent_tool.run_worker", lambda persona, prompt: "结论: 正常")
    done = []
    set_on_done(lambda worker, status, summary: done.append((worker, status, summary)))

    t0 = time.time()
    out = spawn_worker("specialist-sre", "d", "p", background=True)
    assert time.time() - t0 < 1.0      # 不阻塞
    assert "后台" in out
    assert _wait_done(done), "on_done 回调应在后台 worker 完成后被调用"
    assert done[0][0] == "specialist-sre"
    assert done[0][1] == "completed"
    assert done[0][2] == "结论: 正常"


def test_background_failure_reports_failed(monkeypatch):
    set_personas({"specialist-sre": _persona()})

    def boom(persona, prompt):
        raise RuntimeError("工具执行失败")
    monkeypatch.setattr("agent_tool.run_worker", boom)
    done = []
    set_on_done(lambda worker, status, summary: done.append((worker, status, summary)))

    spawn_worker("specialist-sre", "d", "p", background=True)
    assert _wait_done(done)
    assert done[0][1] == "failed"
    assert "执行失败" in done[0][2]


def test_default_on_done_pushes_notification_queue():
    """未自定义 on_done 时，终态写入通知队列（chat SSE 轮询 task_notification）。"""
    set_on_done()  # 恢复默认
    _notify("reporter", "completed", "报告完成")
    items = drain_notifications()
    assert items and items[-1]["type"] == "task_notification"
    assert items[-1]["data"]["worker"] == "reporter"
    assert items[-1]["data"]["status"] == "completed"


def test_background_default_callback_drains_frame(monkeypatch):
    """后台完成走默认回调 → drain_notifications 能取到 task_notification frame。"""
    set_personas({"specialist-sre": _persona()})
    set_on_done()  # 默认回调
    monkeypatch.setattr("agent_tool.run_worker", lambda persona, prompt: "后台调查完成")
    spawn_worker("specialist-sre", "d", "p", background=True)
    frame = None
    for _ in range(100):
        items = drain_notifications()
        if items:
            frame = items[0]
            break
        time.sleep(0.02)
    assert frame is not None
    assert frame["type"] == "task_notification"
    assert frame["data"]["worker"] == "specialist-sre"
    assert frame["data"]["status"] == "completed"
