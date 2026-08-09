import pytest
from function_calling import make_tools_schema, exec_tool_with_guard, WHITELIST_READONLY


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
