"""P7.1 Tool Registry MVP — TDD 测试（V9.3 Phase7，注册不执行）。

覆盖 P7.1 设计的 T1-T5 验收条件与 F1-F5 红线：
- T1 注册与校验
- T2 Capability gate（§31）
- T3 Planner 选择约束（§30）
- T4 边界安全（read_only / execute disabled / owner / 无隐藏 Tool）
- T5 Registry Integrity（contract_version / risk immutable / capability 重审）
"""
import pytest

from tool_registry import (
    ToolDefinition,
    ToolRegistry,
    RISK_LEVELS,
    LIFECYCLE_STATUSES,
    validate_tool_definition,
    minimum_tool_ids,
    init_default_tool_registry,
)


# ═══════════════════════════════════════════════════════
#  Helpers
# ═══════════════════════════════════════════════════════

def make_query_logs(**over):
    base = dict(
        tool_id="query_logs.v1",
        version="1.0.0",
        contract_version="v1",
        domain="observability",
        name="query_logs",
        category="query",
        description="查询日志",
        owner="query-api",
        lifecycle_status="active",
        read_only=True,
        baseline_risk="R0",
        risk_level="R0",
        evidence_required=True,
        capability="observability.logs.read",
        required_capability="observability.logs.read",
        availability="per-cluster",
        allowed_scope="cluster",
        input_schema={"type": "object", "properties": {}},
        output_schema={"type": "object"},
        timeout_class="fast",
        timeout=30,
        retry=2,
        backend="query-api",
        planner_selectable=True,
        automatic=True,
        execution_state="enabled",
    )
    base.update(over)
    return ToolDefinition(**base)


def reset_registry():
    ToolRegistry._tools = {}


@pytest.fixture(autouse=True)
def _reset():
    reset_registry()
    yield
    reset_registry()


# ═══════════════════════════════════════════════════════
#  T1 注册与校验
# ═══════════════════════════════════════════════════════

def test_t1_valid_definition_registers():
    t = make_query_logs()
    assert validate_tool_definition(t) is None
    ToolRegistry.register(t)
    assert ToolRegistry.get("query_logs.v1") is not None


def test_t1_missing_required_capability_rejected():
    t = make_query_logs(capability="", required_capability="")
    err = validate_tool_definition(t)
    assert err is not None
    assert "capability" in err


def test_t1_missing_allowed_scope_rejected():
    t = make_query_logs(allowed_scope="")
    assert validate_tool_definition(t) is not None


def test_t1_missing_owner_rejected():
    t = make_query_logs(owner="")
    assert validate_tool_definition(t) is not None
    assert "owner" in validate_tool_definition(t)


def test_t1_contract_version_defaults_to_v1():
    t = make_query_logs()
    assert t.contract_version == "v1"


def test_t1_duplicate_tool_id_rejected():
    t1 = make_query_logs()
    ToolRegistry.register(t1)
    with pytest.raises(ValueError):
        ToolRegistry.register(make_query_logs())


def test_t1_illegal_status_rejected():
    t = make_query_logs(lifecycle_status="bogus")
    assert validate_tool_definition(t) is not None


# ═══════════════════════════════════════════════════════
#  T2 Capability gate（§31）
# ═══════════════════════════════════════════════════════

def test_t2_principal_without_capability_cannot_select():
    t = make_query_logs()
    ToolRegistry.register(t)
    # principal 无 capability
    assert ToolRegistry.is_selectable("query_logs.v1", capabilities=set(), scope="cluster") is False


def test_t2_required_capability_subset_allowed():
    t = make_query_logs()
    ToolRegistry.register(t)
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.logs.read"}, scope="cluster"
    ) is True


def test_t2_wrong_capability_not_allowed():
    t = make_query_logs()
    ToolRegistry.register(t)
    # logs.read 不能选成 metrics 相关 Tool
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.metrics.read"}, scope="cluster"
    ) is False


def test_t2_scope_not_covered_rejected():
    t = make_query_logs(allowed_scope="cluster")
    ToolRegistry.register(t)
    # 请求 global scope，但 tool 只允许 cluster
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.logs.read"}, scope="global"
    ) is False


# ═══════════════════════════════════════════════════════
#  T3 Planner 选择约束（§30）
# ═══════════════════════════════════════════════════════

def test_t3_unregistered_tool_not_selectable():
    assert ToolRegistry.is_selectable("nonexistent.v1", capabilities={"x"}, scope="cluster") is False


def test_t3_non_active_tool_not_selectable():
    t = make_query_logs(lifecycle_status="draft")
    ToolRegistry.register(t)
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.logs.read"}, scope="cluster"
    ) is False


def test_t3_planner_selectable_false_not_selectable():
    t = make_query_logs(planner_selectable=False)
    ToolRegistry.register(t)
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.logs.read"}, scope="cluster"
    ) is False


def test_t3_readonly_tool_selectable():
    t = make_query_logs(read_only=True)
    ToolRegistry.register(t)
    assert ToolRegistry.is_selectable(
        "query_logs.v1", capabilities={"observability.logs.read"}, scope="cluster"
    ) is True


def test_t3_execute_tool_not_selectable_even_with_capability():
    # execute_shell 注册但 planner_selectable=false → 不可被 Planner 选
    t = ToolDefinition(
        tool_id="execute_shell.v1", version="1.0.0", contract_version="v1",
        domain="execution", name="execute_shell", category="execution",
        description="执行 shell（Phase7 仅注册，禁用）", owner="query-api",
        lifecycle_status="active", read_only=False, baseline_risk="R4", risk_level="R4",
        evidence_required=True, capability="execution.shell",
        required_capability="execution.shell", availability="per-cluster",
        allowed_scope="cluster", input_schema={}, output_schema={},
        timeout_class="slow", timeout=30, retry=0, backend="none",
        planner_selectable=False, automatic=False, execution_state="disabled",
    )
    ToolRegistry.register(t)
    assert ToolRegistry.is_selectable(
        "execute_shell.v1", capabilities={"execution.shell"}, scope="cluster"
    ) is False


# ═══════════════════════════════════════════════════════
#  T4 边界安全
# ═══════════════════════════════════════════════════════

def test_t4_execute_tool_forced_R4_disabled():
    # execute_k8s/execute_shell 必须 R4 + planner_selectable=false + automatic=false + disabled
    init_default_tool_registry()
    for tid in ("execute_k8s.v1", "execute_shell.v1"):
        t = ToolRegistry.get(tid)
        assert t is not None, f"{tid} 未注册"
        assert t.baseline_risk == "R4"
        assert t.risk_level == "R4"
        assert t.planner_selectable is False
        assert t.automatic is False
        assert t.execution_state == "disabled"


def test_t4_agent_cannot_register_hidden_tool():
    # Agent 不能注册未在 registry 的隐藏 Tool：注册必经 validate
    t = ToolDefinition(
        tool_id="hidden_agent_tool.v1", version="1.0.0", contract_version="v1",
        domain="execution", name="hidden_tool", category="execution",
        description="agent 试图注册的隐藏工具", owner="agent",
        lifecycle_status="active", read_only=False, baseline_risk="R2", risk_level="R2",
        evidence_required=True, capability="execution.hidden",
        required_capability="execution.hidden", availability="per-cluster",
        allowed_scope="cluster", input_schema={}, output_schema={},
        timeout_class="slow", timeout=30, retry=0, backend="none",
        planner_selectable=True, automatic=True, execution_state="enabled",
    )
    # 校验应拒绝：read_only=False 但非 execute_* 白名单 + capability 未注册
    assert validate_tool_definition(t) is not None


def test_t4_owner_required_nonempty():
    assert validate_tool_definition(make_query_logs(owner="")) is not None


def test_t4_no_duplicate_execution_path():
    # execute_shell 不得绑定可执行 func（注册不执行）
    t = ToolRegistry.get("execute_shell.v1")
    assert not hasattr(t, "func") or t.func is None


# ═══════════════════════════════════════════════════════
#  T5 Registry Integrity
# ═══════════════════════════════════════════════════════

def test_t5_risk_level_immutable_after_active():
    # 注册 R2，激活后尝试降到 R1（降级）→ 拒绝
    t = make_query_logs(risk_level="R2", baseline_risk="R2")
    ToolRegistry.register(t)
    assert ToolRegistry.update(t.tool_id, risk_level="R1") is False


def test_t5_risk_can_elevate():
    t = make_query_logs(risk_level="R0")
    ToolRegistry.register(t)
    # 升级 R0→R1 允许
    assert ToolRegistry.update(t.tool_id, risk_level="R1") is True
    assert ToolRegistry.get(t.tool_id).risk_level == "R1"


def test_t5_active_capability_change_requires_reapproval():
    t = make_query_logs()
    ToolRegistry.register(t)
    # active 修改 capability → 拒绝（需重新审批）
    assert ToolRegistry.update(t.tool_id, capability="observability.metrics.read") is False


def test_t5_contract_version_mismatch_rejected():
    t = make_query_logs()
    ToolRegistry.register(t)
    # 试图用不兼容 contract_version 更新 → 拒绝
    assert ToolRegistry.update(t.tool_id, contract_version="v2") is False


def test_t5_lifecycle_transition_allowed():
    t = make_query_logs()
    ToolRegistry.register(t)
    assert ToolRegistry.update(t.tool_id, lifecycle_status="deprecated") is True


# ═══════════════════════════════════════════════════════
#  最低生产 Tool 清单
# ═══════════════════════════════════════════════════════

def test_minimum_tool_list_registered():
    init_default_tool_registry()
    for tid in minimum_tool_ids():
        assert ToolRegistry.get(tid) is not None, f"缺最低生产 Tool: {tid}"
