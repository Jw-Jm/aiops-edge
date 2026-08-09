import pytest
from llm_mock import is_mock_enabled, mock_llm_response, should_skip_llm
from llm_mock import mock_llm_decision, mock_coordinator_plan, mock_reviewer_result


def test_mock_disabled_by_default(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert is_mock_enabled() is False


def test_mock_enabled_when_true(monkeypatch):
    monkeypatch.setenv("LLM_MOCK", "true")
    assert is_mock_enabled() is True


def test_mock_response_shape():
    resp = mock_llm_response("who is the caller?")
    assert isinstance(resp, str) and len(resp) > 0
    assert "RCA" in resp or "analysis" in resp.lower()


def test_should_skip_llm_skips_when_no_key_and_not_mock(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert should_skip_llm(None) is True
    assert should_skip_llm({}) is True
    assert should_skip_llm({"api_key": ""}) is True
    assert should_skip_llm({"api_key": "sk-xxx"}) is False


def test_should_skip_llm_not_skip_when_mock_even_without_key(monkeypatch):
    monkeypatch.setenv("LLM_MOCK", "true")
    assert should_skip_llm(None) is False
    assert should_skip_llm({}) is False


def test_mock_decision_first_tool_then_final():
    # 第一次调用应返回 query_metrics 工具决策
    d1 = mock_llm_decision([], [])
    assert d1["type"] == "tool"
    # 第二次（带已有消息）返回 final
    d2 = mock_llm_decision([{"role": "assistant", "content": "已调工具"}], [])
    assert d2["type"] == "final"
    assert isinstance(d2.get("content"), str)


def test_mock_coordinator_plan_shape():
    plan = mock_coordinator_plan()
    assert isinstance(plan, list) and len(plan) >= 2
    assert "task_type" in plan[0] and "task_id" in plan[0]


def test_mock_reviewer_merges():
    out = mock_reviewer_result({"t1": {"conclusion": "a"}, "t2": {"conclusion": "b"}})
    assert isinstance(out, str) and "b" in out
