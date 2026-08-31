import os
import subprocess
import sys

import pytest
from llm_mock import is_mock_enabled, mock_llm_response, should_skip_llm
from llm_mock import mock_llm_decision, mock_coordinator_plan, mock_reviewer_result


def test_mock_disabled_by_default(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert is_mock_enabled() is False


def test_production_rejects_mock_before_startup():
    """Production must not boot with deterministic/mock LLM output enabled."""
    env = os.environ.copy()
    env.pop("AIOPS_DEPLOYMENT_MODE", None)
    env["AIOPS_ENV"] = "production"
    env["LLM_MOCK"] = "true"
    result = subprocess.run(
        [sys.executable, "-c", "import main"],
        cwd=os.path.dirname(os.path.dirname(__file__)),
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode != 0
    assert "LLM_MOCK=true is forbidden" in (result.stdout + result.stderr)


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


def test_mock_reviewer_empty():
    out = mock_reviewer_result({})
    assert isinstance(out, str) and "无子结论" in out


def test_mock_decision_drives_run_tool_loop_end_to_end():
    """mock 决策注入 run_tool_loop 应完整驱动循环：调一次工具→final。"""
    from function_calling import run_tool_loop
    from skill_registry import ToolRegistry
    if not ToolRegistry.get("query_metrics"):
        ToolRegistry.register(name="query_metrics", description="d",
                              params={"service": {"type": "string", "required": False, "default": "", "desc": "s"}},
                              cls_="safe")(lambda service="x": "ok")
    tools = [ToolRegistry.get("query_metrics")]
    res = run_tool_loop(mock_llm_decision, tools, "诊断服务")
    assert res["tool_calls"] == 1
    assert res["truncated"] is False
    assert res["trace"][0]["tool"] == "query_metrics"
    assert "mock" in res["final"]
