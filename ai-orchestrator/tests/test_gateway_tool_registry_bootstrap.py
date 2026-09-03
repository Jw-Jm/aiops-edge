"""Gateway composition must load the canonical internal-query Tool registry.

P2-F2: 原测试仅检查 orchestrator.py 源码文本含 init_default_tool_registry()
（源码字符串伪门禁）。本测试改为运行时断言：真正执行注册、查询 registry，
验证 required tools 注册、capability 语义与 query-api backend 路由。
"""

import pytest

from tool_registry import CONTRACT_VERSION, REGISTRY_TOOL_IDS, ToolRegistry, init_default_tool_registry

# 生产注册清单快照（期望值，运行时对比）
_READONLY_QUERY_IDS = [t for t in REGISTRY_TOOL_IDS if not t.startswith("execute_")]
_EXECUTE_IDS = [t for t in REGISTRY_TOOL_IDS if t.startswith("execute_")]


@pytest.fixture(autouse=True)
def _fresh_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    yield


def test_gateway_composition_registers_all_required_tools_runtime():
    """运行时执行生产注册函数后，registry 必须包含全部生产工具（不再检查源码字符串）。"""
    init_default_tool_registry()
    registered = {t.tool_id for t in ToolRegistry.list_all()}
    assert set(REGISTRY_TOOL_IDS) <= registered, (
        f"missing tools: {set(REGISTRY_TOOL_IDS) - registered}"
    )


def test_readonly_query_tools_are_selectable_and_backed_by_query_api():
    """查询类工具：capability==required、read_only、selectable、backend=query-api
    （canonical internal query adapter 路由），且不暴露任何执行面。"""
    init_default_tool_registry()
    for tool in ToolRegistry.list_all():
        if tool.tool_id.startswith("execute_"):
            continue
        assert tool.read_only is True, f"{tool.tool_id} must be read-only"
        # 只读工具 backend 仅允许：query-api（canonical 主路径）、k8sgpt（只读诊断）、
        # knowledge（知识库检索）。任何执行类 backend 不得出现。
        assert tool.backend in {"query-api", "k8sgpt", "knowledge"}, (
            f"{tool.tool_id} unexpected read-only backend {tool.backend}"
        )
        assert tool.capability == tool.required_capability, (
            f"{tool.tool_id} capability must equal required_capability"
        )
        assert tool.planner_selectable is True, f"{tool.tool_id} must be planner-selectable"
        assert tool.lifecycle_status == "active", f"{tool.tool_id} must be active"
        assert tool.category != "execution"
        # 选中查询工具必须要求对应 capability 才能被 selectable
        assert ToolRegistry.is_selectable(tool.tool_id, capabilities={tool.required_capability})
        assert not ToolRegistry.is_selectable(tool.tool_id, capabilities={"nonsense.capability"})


def test_execute_tools_registered_but_disabled_not_planner_selectable():
    """执行类工具仅注册且显式 disabled：planner 不可选、自动执行关闭。"""
    init_default_tool_registry()
    for tool_id in _EXECUTE_IDS:
        tool = ToolRegistry.get(tool_id)
        assert tool is not None, f"{tool_id} must be registered"
        assert tool.execution_state == "disabled", f"{tool_id} must stay disabled"
        assert tool.planner_selectable is False, f"{tool_id} must not be planner-selectable"
        assert tool.automatic is False, f"{tool_id} must not auto-execute"
        assert not ToolRegistry.is_selectable(tool_id, capabilities={tool.required_capability})


def test_init_is_idempotent():
    init_default_tool_registry()
    first = {t.tool_id for t in ToolRegistry.list_all()}
    init_default_tool_registry()  # 幂等：已注册时不重复注册（否则 duplicate 抛错）
    second = {t.tool_id for t in ToolRegistry.list_all()}
    assert first == second


def test_registry_snapshot_matches_contract_version():
    init_default_tool_registry()
    for tool in ToolRegistry.list_all():
        assert tool.contract_version == CONTRACT_VERSION
