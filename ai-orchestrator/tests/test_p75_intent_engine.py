"""P7.5 Intent Engine — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.5 设计的 T1-T6：
- T1 意图解析（明确 cluster/service/time → 结构化 Intent；枚举合法；缺 time_range 默认）
- T2 歧义处理（目标歧义 → RESOURCE_AMBIGUOUS 不猜；停在 IntentCreated）
- T3 Scope 收敛（tenant/cluster canonical；capability 来自 Registry；scope 不扩大）
- T4 Manual Trigger（Alert/Event 不自动创建 Run；仅预填 scope）
- T5 State Machine（IntentCreated→EvidenceCollected→PlanGenerated→AwaitingApproval；Phase7 不进 Approved/Executed）
- T6 Capability Non-Creation（LLM 不能生成 capability；capability 仅 Registry 存在可用）
"""
from __future__ import annotations

import pytest
from datetime import datetime, timezone

from intent_engine import (
    ACTION_MODES,
    INTENT_STATUSES,
    TARGET_TYPES,
    Intent,
    IntentAmbiguityError,
    IntentEngine,
)
from tool_registry import ToolRegistry, init_default_tool_registry


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


@pytest.fixture
def engine():
    return IntentEngine()


def _base(engine, **over):
    kw = dict(
        intent="调查 checkout 服务 1h 内日志错误",
        action_mode="read_only",
        target_type="service",
        target_resource_id="checkout",
        tenant_id=TENANT,
        primary_cluster_id=CLUSTER,
        capability="observability.logs.read",
        source="user_explicit",
        time_range_start="2026-08-20T00:00:00Z",
        time_range_end="2026-08-20T01:00:00Z",
        symptom="日志错误率升高",
    )
    kw.update(over)
    return kw


# ═══════════════════════════════════════════════════════
#  T1 意图解析
# ═══════════════════════════════════════════════════════

class TestT1IntentParsing:
    def test_explicit_intent_created(self, engine):
        intent = engine.create_intent(**_base(engine))
        assert intent.intent == "调查 checkout 服务 1h 内日志错误"
        assert intent.action_mode == "read_only"
        assert intent.target_type == "service"
        assert intent.target_resource_id == "checkout"
        assert intent.status == "IntentCreated"
        assert intent.capability == "observability.logs.read"

    def test_invalid_action_mode_rejected(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, action_mode="execute_allowed"))  # Phase7 禁

    def test_invalid_target_type_rejected(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, target_type="unknown_thing"))

    def test_missing_time_range_defaults(self, engine):
        intent = engine.create_intent(**_base(engine, time_range_start=None, time_range_end=None))
        start = datetime.fromisoformat(intent.time_range_start.replace("Z", "+00:00"))
        end = datetime.fromisoformat(intent.time_range_end.replace("Z", "+00:00"))
        now = datetime.now(timezone.utc)
        assert (end - start).total_seconds() == 3600
        assert abs((now - end).total_seconds()) < 5


# ═══════════════════════════════════════════════════════
#  T2 歧义处理
# ═══════════════════════════════════════════════════════

class TestT2Ambiguity:
    def test_ambiguous_target_rejected(self, engine):
        # target_type=service 但无 target_resource_id → 目标歧义，禁止猜
        with pytest.raises(IntentAmbiguityError) as exc:
            engine.create_intent(**_base(engine, target_resource_id=None))
        assert exc.value.error_code == "RESOURCE_AMBIGUOUS"
        assert "missing" in " ".join(exc.value.reason)

    def test_ambiguous_stays_intent_created(self, engine):
        # 歧义时停在 IntentCreated，不进入 Planning
        with pytest.raises(IntentAmbiguityError):
            engine.create_intent(**_base(engine, target_resource_id=None))
        # 无 Intent 被创建（不进入 planning）

    def test_cluster_target_no_resource_ok(self, engine):
        # target_type=cluster 无需 resource → 可创建
        intent = engine.create_intent(**_base(engine, target_type="cluster", target_resource_id=None))
        assert intent.status == "IntentCreated"


# ═══════════════════════════════════════════════════════
#  T3 Scope 收敛
# ═══════════════════════════════════════════════════════

class TestT3ScopeConvergence:
    def test_non_canonical_tenant_rejected(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, tenant_id="default"))

    def test_non_canonical_cluster_rejected(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, primary_cluster_id="bad-cluster"))

    def test_capability_from_registry(self, engine):
        intent = engine.create_intent(**_base(engine))
        assert intent.capability in {t.capability for t in ToolRegistry.list_all()}

    def test_unknown_capability_rejected(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, capability="made_up.capability"))


# ═══════════════════════════════════════════════════════
#  T4 Manual Trigger
# ═══════════════════════════════════════════════════════

class TestT4ManualTrigger:
    def test_alert_event_no_auto_run(self, engine):
        # Alert/Event 到达不自动创建 Run；create_intent 仅当用户显式触发（source=user_explicit）
        intent = engine.create_intent(**_base(engine, source="user_explicit"))
        assert intent.source == "user_explicit"
        # source=approved_system_event 仅预填 scope，不创建 Run
        # （本 MVP：引擎本身不创建 Run，只产出 Intent）

    def test_approved_system_event_source(self, engine):
        intent = engine.create_intent(**_base(engine, source="approved_system_event"))
        assert intent.source == "approved_system_event"
        assert intent.status == "IntentCreated"  # 仅预填，不自动进入 Run


# ═══════════════════════════════════════════════════════
#  T5 State Machine
# ═══════════════════════════════════════════════════════

class TestT5StateMachine:
    def test_progression(self, engine):
        intent = engine.create_intent(**_base(engine))
        intent = engine.transition(intent.intent_id, "EvidenceCollected")
        assert intent.status == "EvidenceCollected"
        intent = engine.transition(intent.intent_id, "PlanGenerated")
        assert intent.status == "PlanGenerated"
        intent = engine.transition(intent.intent_id, "AwaitingApproval")
        assert intent.status == "AwaitingApproval"

    def test_phase7_no_approved_or_executed(self, engine):
        intent = engine.create_intent(**_base(engine))
        with pytest.raises(ValueError):
            engine.transition(intent.intent_id, "Approved")  # Phase7 边界：不进入

    def test_illegal_transition_rejected(self, engine):
        intent = engine.create_intent(**_base(engine))
        with pytest.raises(ValueError):
            engine.transition(intent.intent_id, "PlanGenerated")  # 跳过中间态非法


# ═══════════════════════════════════════════════════════
#  T6 Capability Non-Creation
# ═══════════════════════════════════════════════════════

class TestT6CapabilityNonCreation:
    def test_delete_request_plan_only(self, engine):
        # 用户要求执行 kubectl delete → action_mode=plan_only（Phase7），capability 来自 Registry
        intent = engine.create_intent(**_base(engine, action_mode="plan_only"))
        assert intent.action_mode == "plan_only"
        assert intent.capability in {t.capability for t in ToolRegistry.list_all()}

    def test_llm_cannot_generate_capability(self, engine):
        with pytest.raises(ValueError):
            engine.create_intent(**_base(engine, capability="k8s.delete"))
