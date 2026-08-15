import pytest
from execution_gate import check_tool_executable
from skill_registry import ToolDef


def _tool(name, cls="safe", requires_approval=False):
    return ToolDef(name=name, description="d", func=lambda: "ok", cls=cls, requires_approval=requires_approval)


def test_safe_tool_direct_without_approval():
    t = _tool("query_metrics", cls="safe")
    ok, reason = check_tool_executable(t, approved=False)
    assert ok, reason


def test_mutating_needs_approval():
    t = _tool("vm_operate", cls="mutating", requires_approval=True)
    ok, _ = check_tool_executable(t, approved=False)
    assert not ok
    ok, _ = check_tool_executable(t, approved=True)
    assert ok


def test_mutating_without_flag_still_needs_approval():
    # 即使未显式 requires_approval，mutating 也必须审批
    t = _tool("restart", cls="mutating")
    ok, _ = check_tool_executable(t, approved=False)
    assert not ok


def test_dangerous_forced_approval():
    t = _tool("execute_shell", cls="dangerous")
    ok, _ = check_tool_executable(t, approved=False)
    assert not ok
    ok, _ = check_tool_executable(t, approved=True)
    assert ok


def test_requires_approval_flag_needs_approval():
    t = _tool("deploy", cls="safe", requires_approval=True)
    ok, _ = check_tool_executable(t, approved=False)
    assert not ok
    ok, _ = check_tool_executable(t, approved=True)
    assert ok


def test_gate_returns_clear_reason():
    t = _tool("execute_shell", cls="dangerous")
    ok, reason = check_tool_executable(t, approved=False)
    assert not ok and "dangerous" in reason.lower()


def test_unknown_tool_rejected():
    ok, _ = check_tool_executable(None, approved=True)
    assert not ok


def test_real_tools_classified():
    """验证实际注册的工具已正确分级（C1 补全）。"""
    from skill_registry import ToolRegistry
    # D1 后 init_skills() 为占位，工具注册改由各内置 skill 模块负责
    from skills.observability import register_observability_skill
    from skills.infra import register_infra_skill
    from skills.rca_skill import register_rca_skill
    from skills.rag_skill import register_rag_skill
    from skills.vm_ops import register_vm_skill
    from skills.alert_ops import register_alert_skill
    from skills.automation import register_automation_skill
    from skills.diagnose import register_diagnose_skill
    for fn in (register_observability_skill, register_infra_skill, register_rca_skill,
               register_rag_skill, register_vm_skill, register_alert_skill,
               register_automation_skill, register_diagnose_skill):
        fn()
    vm = ToolRegistry.get("vm_operate")
    assert vm is not None and vm.cls == "mutating", f"vm_operate cls={vm.cls if vm else None}"
    shell = ToolRegistry.get("execute_shell")
    assert shell is not None and shell.cls == "dangerous", f"execute_shell cls={shell.cls if shell else None}"
    qm = ToolRegistry.get("query_metrics")
    assert qm is not None and qm.cls == "safe"
    # mutating/dangerous 工具经闸门必须审批
    ok, _ = check_tool_executable(vm, approved=False)
    assert not ok
    ok, _ = check_tool_executable(shell, approved=False)
    assert not ok
