from skill_registry import ToolDef, ToolRegistry
import hardware_tools  # noqa: F401  触发硬件工具注册


def test_tool_class_metadata():
    """ToolDef 扩展 cls/scope/when_to_use/origin 字段。"""
    t = ToolDef(name="k8s_get", description="x", func=lambda: None, cls="safe", scope="manager")
    assert t.cls in ("safe", "mutating", "dangerous")
    assert t.scope in ("host", "manager")
    assert t.when_to_use == ""
    assert t.origin == "builtin"


def test_new_tools_registered():
    """二期新增硬件/部件查询工具已注册。"""
    names = {x.name for x in ToolRegistry.list_all()}
    for tool in ["snmp_query", "snmp_health", "ipmi_health", "node_health"]:
        assert tool in names, f"tool {tool} not registered"


def test_tool_class_values():
    """只读工具应为 safe Class，写操作工具需标记 mutating/dangerous。"""
    by_name = {x.name: x for x in ToolRegistry.list_all()}
    for tool_name in ["snmp_query", "ipmi_health", "node_health"]:
        assert by_name[tool_name].cls == "safe", f"{tool_name} should be safe"
