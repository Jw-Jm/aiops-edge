"""investigator.maybe_investigate hermetic 测试:
monkeypatch agent_tool.run_worker + 注入假 personas, 不触网、不落库、不碰 ChromaDB。
覆盖: 低级别告警返回 None / 去重窗口内重复告警返回 None / 正常告警调用 run_worker
并返回报告 / worker 异常时返回 None。
"""
import threading

import pytest

import investigator
from persona_registry import Persona

FAKE_PERSONA = Persona(
    name="incident-investigator",
    description="告警根因调查专员",
    when_to_use="告警触发后的根因调查",
    system_prompt="你是告警根因调查专员。",
    tools=["case_search", "rca_analyze"],
    permission_mode="read-only",
    max_turns=40,
)


@pytest.fixture(autouse=True)
def _reset(monkeypatch):
    """每个用例复位: 门控默认开、去重表清空、并发信号量重建、注入假 persona。"""
    monkeypatch.setenv("INVESTIGATOR_ENABLED", "1")
    with investigator._lock:
        investigator._last_fired.clear()
    investigator._semaphore = threading.BoundedSemaphore(investigator.MAX_CONCURRENT)
    monkeypatch.setattr(investigator, "_PERSONAS", {"incident-investigator": FAKE_PERSONA})
    yield
    monkeypatch.delenv("INVESTIGATOR_ENABLED", raising=False)


def test_low_severity_returns_none(monkeypatch):
    """info < min_severity(warning) → 不调用 run_worker, 返回 None。"""
    called = []
    monkeypatch.setattr(investigator.agent_tool, "run_worker",
                        lambda persona, prompt: called.append(prompt) or "报告")

    assert investigator.maybe_investigate("high-cpu", "info", {}) is None
    assert called == []


def test_duplicate_within_dedup_window_returns_none(monkeypatch):
    """同一 rule 在 300s 去重窗口内重复告警 → 第二次返回 None。"""
    called = []
    monkeypatch.setattr(investigator.agent_tool, "run_worker",
                        lambda persona, prompt: called.append(prompt) or "报告")
    monkeypatch.setattr(investigator, "_store_report", lambda *a: "(已入库)")

    first = investigator.maybe_investigate("high-cpu", "warning", {})
    second = investigator.maybe_investigate("high-cpu", "critical", {})

    assert first is not None
    assert second is None
    assert len(called) == 1


def test_normal_alert_calls_run_worker_and_returns_report(monkeypatch):
    """正常告警 → 调用 incident-investigator worker, 返回报告并附带入库说明。"""
    ran = []
    stored = []

    def fake_run(persona, prompt):
        ran.append((persona.name, prompt))
        return "根因: CPU 争抢导致 P99 上升\n处置建议: 扩容"

    monkeypatch.setattr(investigator.agent_tool, "run_worker", fake_run)
    monkeypatch.setattr(
        investigator, "_store_report",
        lambda rule, severity, payload, report: stored.append((rule, severity)) or "(已入库)")

    result = investigator.maybe_investigate(
        "high-cpu", "critical", {"service": "api", "summary": "CPU 使用率 95%"})

    assert result is not None
    assert "根因: CPU 争抢" in result
    assert "(已入库)" in result
    assert ran and ran[0][0] == "incident-investigator"
    assert "high-cpu" in ran[0][1]
    assert "critical" in ran[0][1]
    assert stored == [("high-cpu", "critical")]


def test_worker_exception_returns_none(monkeypatch):
    """worker 抛异常 → 不抛出, 返回 None。"""

    def boom(persona, prompt):
        raise RuntimeError("worker boom")

    monkeypatch.setattr(investigator.agent_tool, "run_worker", boom)

    assert investigator.maybe_investigate("high-cpu", "critical", {}) is None
