import pytest
from function_calling import make_tools_schema, exec_tool_with_guard, run_tool_loop, WHITELIST_READONLY


def _dummy_safe(**kw):
    from skill_registry import ToolDef
    return ToolDef(name=kw.get("name", "query_metrics"), description="d", func=lambda service="x": "ok",
                   params={"service": {"type": "string", "required": True, "default": "", "desc": "s"}},
                   cls=kw.get("cls", "safe"))


def test_make_tools_schema_shape():
    t = _dummy_safe(name="query_metrics")
    schema = make_tools_schema([t])
    assert schema[0]["type"] == "function"
    assert schema[0]["function"]["name"] == "query_metrics"
    assert "service" in schema[0]["function"]["parameters"]["properties"]


def test_exec_safe_tool_runs():
    t = _dummy_safe()
    out = exec_tool_with_guard(t, {"service": "svc"}, WHITELIST_READONLY)
    assert out == "ok"


def test_exec_mutating_rejected():
    t = _dummy_safe(cls="mutating")
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "拒绝" in out and "mutating" in out


def test_exec_not_in_whitelist_rejected():
    t = _dummy_safe(name="dangerous_tool")
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "拒绝" in out


def test_exec_requires_approval_rejected():
    from skill_registry import ToolDef
    t = ToolDef(name="restart", description="d", func=lambda: "x", cls="mutating", requires_approval=True)
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "审批" in out or "拒绝" in out


def test_exec_dangerous_rejected():
    t = _dummy_safe(cls="dangerous")
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "拒绝" in out and "dangerous" in out


def test_whitelist_has_core_readonly_tools():
    for name in ["query_metrics", "query_traces", "query_topology", "get_service_list", "get_infrastructure"]:
        assert name in WHITELIST_READONLY


def test_make_tools_schema_required_null_when_empty():
    """无必填参数时 required 不应为空数组（OpenAI 会报错），当前实现输出 null。"""
    from skill_registry import ToolDef
    t = ToolDef(name="noargs", description="d", func=lambda: "x", params={}, cls="safe")
    schema = make_tools_schema([t])
    assert schema[0]["function"]["parameters"]["required"] is None


# ── run_tool_loop 护栏测试 ───────────────────────────────
def _register_dummy_tool():
    from skill_registry import ToolRegistry
    if not ToolRegistry.get("query_metrics"):
        ToolRegistry.register(name="query_metrics", description="d",
                              params={"service": {"type": "string", "required": False, "default": "", "desc": "s"}},
                              cls_="safe")(lambda service="x": "ok")
    return ToolRegistry.get("query_metrics")


def _final_decision(messages, tools):
    return {"type": "final", "content": "结论"}


def test_run_tool_loop_final_early_return():
    t = _register_dummy_tool()
    res = run_tool_loop(_final_decision, [t], "问题")
    assert res["final"] == "结论"
    assert res["truncated"] is False
    assert res["tool_calls"] == 0
    assert res["steps"] == 1


def test_run_tool_loop_max_steps_truncated():
    """决策器永远返回 tool 调用 → 触发 max_steps=6 截断，truncated=True。"""
    t = _register_dummy_tool()

    def always_tool(messages, tools):
        return {"type": "tool", "name": "query_metrics", "arguments": {}}

    res = run_tool_loop(always_tool, [t], "问题", max_steps=3, max_tool_calls=10)
    assert res["truncated"] is True
    assert res["steps"] == 3
    assert res["tool_calls"] == 3


def test_run_tool_loop_max_tool_calls_truncated():
    """决策器调用工具次数超过 max_tool_calls=2 → 截断，truncated=True。"""
    t = _register_dummy_tool()

    def always_tool(messages, tools):
        return {"type": "tool", "name": "query_metrics", "arguments": {}}

    res = run_tool_loop(always_tool, [t], "问题", max_steps=10, max_tool_calls=2)
    assert res["truncated"] is True
    assert res["tool_calls"] == 2  # 第3次工具调用被拒绝


def test_run_tool_loop_executes_tool_and_traces():
    """mock 决策：先调工具拿结果，再 final → trace 含工具调用记录。"""
    t = _register_dummy_tool()
    seq = [
        {"type": "tool", "name": "query_metrics", "arguments": {}},
        {"type": "final", "content": "基于指标结论"},
    ]
    counter = {"i": 0}

    def seq_decision(messages, tools):
        d = seq[min(counter["i"], len(seq) - 1)]
        counter["i"] += 1
        return d

    res = run_tool_loop(seq_decision, [t], "问题")
    assert res["final"] == "基于指标结论"
    assert res["tool_calls"] == 1
    assert len(res["trace"]) == 1
    assert res["trace"][0]["tool"] == "query_metrics"


def test_run_tool_loop_rejects_mutating_from_llm():
    """即使 LLM 请求调用 mutating 工具（如 execute_shell），也被守卫拒绝，trace 记录拒绝结果。"""
    from skill_registry import ToolRegistry
    ToolRegistry.register(name="execute_shell", description="d", cls_="mutating")(lambda command="": "shall not run")
    t = ToolRegistry.get("execute_shell")
    seq = [
        {"type": "tool", "name": "execute_shell", "arguments": {}},
        {"type": "final", "content": "结论"},
    ]
    counter = {"i": 0}

    def seq_decision(messages, tools):
        d = seq[min(counter["i"], len(seq) - 1)]
        counter["i"] += 1
        return d

    res = run_tool_loop(seq_decision, [t], "问题")
    assert "拒绝" in res["trace"][0]["result"]


# ── llm_fc 真实 LLM 决策解析 ───────────────────────────────
from llm_fc import parse_llm_decision


def test_parse_llm_decision_tool():
    d = parse_llm_decision('{"type":"tool","name":"query_metrics","arguments":{"service":"svc"}}')
    assert d["type"] == "tool" and d["name"] == "query_metrics"
    assert d["arguments"]["service"] == "svc"


def test_parse_llm_decision_final():
    d = parse_llm_decision('{"type":"final","content":"结论"}')
    assert d["type"] == "final"


def test_parse_llm_decision_invalid_defaults_final():
    d = parse_llm_decision("no json")
    assert d["type"] == "final"


def test_parse_llm_decision_tool_missing_name_is_final():
    d = parse_llm_decision('{"type":"tool","arguments":{}}')
    assert d["type"] == "final"


def test_parse_llm_decision_non_dict_json_is_final():
    # LLM 输出合法 JSON 数组/标量时按 final 容错，不崩溃
    d = parse_llm_decision('["a", "b"]')
    assert d["type"] == "final"
    d2 = parse_llm_decision("42")
    assert d2["type"] == "final"
