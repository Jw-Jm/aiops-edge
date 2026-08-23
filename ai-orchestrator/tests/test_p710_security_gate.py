"""P7.10 Security / Negative Tests — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.10 设计的 T1-T4 + 10 项必列 negative tests：
- T1 越权拒绝（unregistered Tool / LLM 改 risk/read_only / wrong capability）
- T2 Cross-Cluster / Scope（cross-cluster / 未知 canonical cluster / 错误映射）
- T3 Budget / 语义（budget exceeded / status exact / ambiguous target）
- T4 后端语义（backend unavailable / permission denied 不降级）
"""
from __future__ import annotations

import pytest

from data_source_mapping import DataSourceMapping
from evidence_hub import EvidenceHub
from intent_engine import IntentEngine, IntentAmbiguityError
from internal_query_client import InternalQueryError
from investigation_state import StateStore
from manual_boundary import ManualBoundary
from planner import Planner
from security_gate import SecurityGate, SecurityViolation
from tool_registry import ToolRegistry, init_default_tool_registry
from tool_result import normalize_tool_result


def _reset_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    init_default_tool_registry()


@pytest.fixture(autouse=True)
def _fresh():
    _reset_registry()
    yield
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()


TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
OTHER_CLUSTER = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"


@pytest.fixture
def gate():
    return SecurityGate(
        registry=ToolRegistry,
        planner=Planner(max_steps=2, max_tools=10),
        intent_engine=IntentEngine(),
        data_source_mapping=DataSourceMapping(registered_clusters={CLUSTER}),
        evidence_hub=EvidenceHub(),
        state_store=StateStore(),
        manual_boundary=ManualBoundary(),
    )


# ═══════════════════════════════════════════════════════
#  T1 越权拒绝
# ═══════════════════════════════════════════════════════

class TestT1PrivilegeEscalation:
    def test_unregistered_tool_rejected(self, gate):
        with pytest.raises(SecurityViolation):
            gate.deny_unregistered_tool("evil_tool.v1")

    def test_llm_cannot_downgrade_risk(self, gate):
        # k8sgpt_diagnose.v1 active R1；LLM 尝试降到 R0 → 拒绝
        with pytest.raises(SecurityViolation):
            gate.deny_risk_downgrade("k8sgpt_diagnose.v1", "R0")

    def test_llm_cannot_change_read_only(self, gate):
        # LLM 尝试把只读 tool 改成非只读 → 拒绝
        with pytest.raises(SecurityViolation):
            gate.deny_readonly_change("query_logs.v1")

    def test_wrong_capability_rejected(self, gate):
        # query_logs.v1 capability=logs.read；用 metrics 操作 → 拒绝
        with pytest.raises(SecurityViolation):
            gate.deny_wrong_capability("query_logs.v1", "metrics")


# ═══════════════════════════════════════════════════════
#  T2 Cross-Cluster / Scope
# ═══════════════════════════════════════════════════════

class TestT2CrossCluster:
    def test_cross_cluster_rejected(self, gate):
        with pytest.raises(SecurityViolation):
            gate.deny_cross_cluster(request_cluster=OTHER_CLUSTER, run_cluster=CLUSTER)

    def test_unknown_canonical_cluster_fail_closed(self, gate):
        with pytest.raises(SecurityViolation):
            gate.deny_unknown_cluster("unknown-cluster-id")

    def test_wrong_mapping_to_canonical_rejected(self, gate):
        # 注册映射指向错误 canonical cluster → 拒绝
        with pytest.raises(SecurityViolation):
            gate.deny_wrong_mapping(OTHER_CLUSTER)


# ═══════════════════════════════════════════════════════
#  T3 Budget / 语义
# ═══════════════════════════════════════════════════════

class TestT3BudgetSemantics:
    def test_budget_exceeded_terminated(self, gate):
        from intent_engine import IntentEngine

        eng = IntentEngine()
        intent = eng.create_intent(
            intent="调查", action_mode="read_only", target_type="service", target_resource_id="checkout",
            tenant_id=TENANT, primary_cluster_id=CLUSTER, capability="observability.logs.read",
            source="user_explicit", time_range_start="t", time_range_end="t",
        )
        plan = gate.planner.propose_plan(
            intent,
            [
                {"step_id": "a", "tool_id": "query_logs.v1", "params": {}, "depends_on": []},
                {"step_id": "b", "tool_id": "query_logs.v1", "params": {}, "depends_on": []},
                {"step_id": "c", "tool_id": "query_logs.v1", "params": {}, "depends_on": []},
            ],
        )  # 3 steps > max_steps=2
        assert plan.status == "budget_exceeded"
        with pytest.raises(SecurityViolation):
            gate.deny_budget_exceeded(plan)

    def test_status_exact(self):
        # ToolResult status 精确（7 态），P7.3 保证
        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="denied")
        tr = normalize_tool_result(
            outcome=outcome,
            tool=ToolRegistry.get("query_logs.v1"),
            tenant_id=TENANT, cluster_id=CLUSTER, request_id="r", query_id="q",
            time_range="t", source_system="query-api",
            started_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
            finished_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
        )
        assert tr.status == "permission_denied"

    def test_ambiguous_target_rejected(self, gate):
        with pytest.raises(SecurityViolation):
            gate.deny_ambiguous_target(target_resource_id=None, target_type="service")


# ═══════════════════════════════════════════════════════
#  T4 后端语义
# ═══════════════════════════════════════════════════════

class TestT4BackendSemantics:
    def test_unavailable_not_downgraded(self):
        outcome = InternalQueryError(kind="unavailable", http_status=503, message="vm down")
        tr = normalize_tool_result(
            outcome=outcome,
            tool=ToolRegistry.get("query_metrics.v1"),
            tenant_id=TENANT, cluster_id=CLUSTER, request_id="r", query_id="q",
            time_range="t", source_system="query-api",
            started_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
            finished_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
        )
        assert tr.status == "unavailable"  # 不降级为 success/healthy

    def test_permission_denied_not_no_data(self):
        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="denied")
        tr = normalize_tool_result(
            outcome=outcome,
            tool=ToolRegistry.get("query_logs.v1"),
            tenant_id=TENANT, cluster_id=CLUSTER, request_id="r", query_id="q",
            time_range="t", source_system="query-api",
            started_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
            finished_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
        )
        assert tr.status == "permission_denied"  # 403 不降级为 no_data


# ═══════════════════════════════════════════════════════
#  T5 Registry Snapshot Hash（防运行期 Tool 偷换）
# ═══════════════════════════════════════════════════════

class TestT5RegistrySnapshot:
    def test_snapshot_contains_version_tools_hash(self, gate):
        snap = gate.snapshot_registry()
        assert snap["version"] == "v1"
        assert isinstance(snap["tools"], list)
        assert snap["hash"]

    def test_untampered_registry_passes(self, gate):
        snap = gate.snapshot_registry()
        gate.assert_registry_integrity(snap)  # 未篡改 → 通过

    def test_tampered_registry_rejected(self, gate):
        snap = gate.snapshot_registry()
        # 运行期偷换：注册一个不在快照里的恶意 Tool
        from tool_registry import ToolDefinition, ToolRegistry

        ToolRegistry._tools.clear()
        ToolRegistry._activated_risk.clear()
        ToolRegistry.register(
            ToolDefinition(
                tool_id="evil.v1", version="1.0.0", contract_version="v1", name="evil",
                description="运行期偷换的恶意 Tool", category="query", owner="attacker",
                lifecycle_status="active", read_only=True,
                baseline_risk="R0", risk_level="R0", capability="observability.logs.read",
                required_capability="observability.logs.read", allowed_scope="cluster",
            )
        )
        with pytest.raises(SecurityViolation):
            gate.assert_registry_integrity(snap)
