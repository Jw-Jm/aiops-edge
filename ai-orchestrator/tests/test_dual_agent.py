import json
import pytest
from dual_agent import parse_subtasks, run_subtask, run_subtasks, merge_review
from llm_mock import mock_llm_decision


def test_parse_subtasks_valid_json():
    raw = '[{"task_id":"t1","task_type":"diagnosis","query":"a"},{"task_id":"t2","task_type":"inspection","query":"b"}]'
    subs = parse_subtasks(raw)
    assert len(subs) == 2 and subs[0]["task_type"] == "diagnosis"


def test_parse_subtasks_fenced_json():
    raw = '```json\n[{"task_id":"t1","task_type":"query","query":"c"}]\n```'
    subs = parse_subtasks(raw)
    assert len(subs) == 1 and subs[0]["task_id"] == "t1"


def test_parse_subtasks_invalid_returns_empty():
    assert parse_subtasks("not json") == []


def test_run_subtask_mock_loop():
    from skills.observability import register_observability_skill
    register_observability_skill()  # 确保 query_metrics 已注册
    subtask = {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "排查"}
    r = run_subtask(subtask, mock_llm_decision, None)
    assert r["task_id"] == "t1"
    assert "mock" in r["conclusion"] or r["conclusion"]
    assert len(r["tool_trace"]) >= 1  # 至少调了一个工具


def test_run_subtasks_parallel_collects_all():
    subs = [
        {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "a"},
        {"task_id": "t2", "task_type": "inspection", "target_service": "unknown", "query": "b"},
    ]
    results = run_subtasks(subs, mock_llm_decision, None)
    assert set(results.keys()) == {"t1", "t2"}


def test_merge_review():
    from llm_mock import mock_reviewer_result
    out = merge_review({"t1": {"conclusion": "a"}}, mock_reviewer_result)
    assert isinstance(out, str) and out


def test_merge_review_none_fallback():
    """reviewer_decision=None 时使用内置拼接兜底。"""
    out = merge_review({"t1": {"conclusion": "a"}, "t2": {"conclusion": "b"}}, None)
    assert isinstance(out, str) and "t1" in out and "a" in out
    assert merge_review({}, None) == "(无子结论)"


def test_parse_subtasks_filters_missing_task_id():
    raw = '[{"task_id":"t1","task_type":"diagnosis","query":"a"},{"task_type":"inspection","query":"b"}]'
    subs = parse_subtasks(raw)
    assert len(subs) == 1 and subs[0]["task_id"] == "t1"


def test_run_subtasks_empty_returns_empty_dict():
    assert run_subtasks([], mock_llm_decision, None) == {}


def test_run_subtasks_on_tool_callback_fires():
    """on_tool 回调应收到 (task_id, task_type, tool_name, result)。"""
    from skills.observability import register_observability_skill
    register_observability_skill()
    calls = []

    def cb(task_id, task_type, name, result):
        calls.append((task_id, task_type, name, result))

    subs = [{"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "a"}]
    run_subtasks(subs, mock_llm_decision, None, on_tool=cb)
    assert len(calls) >= 1
    assert calls[0][0] == "t1"  # task_id
    assert calls[0][1] == "diagnosis"  # task_type
    assert calls[0][2] == "query_metrics"  # tool name


def test_run_subtasks_preserves_order():
    subs = [
        {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "a"},
        {"task_id": "t2", "task_type": "inspection", "target_service": "unknown", "query": "b"},
        {"task_id": "t3", "task_type": "query", "target_service": "unknown", "query": "c"},
    ]
    results = run_subtasks(subs, mock_llm_decision, None)
    assert list(results.keys()) == ["t1", "t2", "t3"]  # 保序
