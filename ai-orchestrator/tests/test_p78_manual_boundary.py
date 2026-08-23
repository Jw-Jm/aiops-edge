"""P7.8 Manual AI Invocation Boundary — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.8 设计的 T1-T4：
- T1 无自动 Run（Alert/Event/页面加载 → 不创建 Run）
- T2 人工触发（认证用户显式触发 → 允许；Run 内 DAG 自动继续）
- T3 Run 生命周期（Run 完成/取消/失败后不自动新建；无独立 Detection Engine）
- T4 System Principal（不能创建 RunInvocation / execution scope / approve plan）
"""
from __future__ import annotations

import pytest

from manual_boundary import ManualBoundary, ManualTriggerDenied


@pytest.fixture
def boundary():
    return ManualBoundary()


# ═══════════════════════════════════════════════════════
#  T1 无自动 Run
# ═══════════════════════════════════════════════════════

class TestT1NoAutoRun:
    def test_alert_no_run(self, boundary):
        with pytest.raises(ManualTriggerDenied):
            boundary.require_user_explicit(source="alert", principal_type="system")

    def test_event_no_run(self, boundary):
        with pytest.raises(ManualTriggerDenied):
            boundary.require_user_explicit(source="event", principal_type="system")

    def test_page_load_no_ai(self, boundary):
        with pytest.raises(ManualTriggerDenied):
            boundary.require_user_explicit(source="page_load", principal_type="system")


# ═══════════════════════════════════════════════════════
#  T2 人工触发
# ═══════════════════════════════════════════════════════

class TestT2ManualTrigger:
    def test_user_explicit_allowed(self, boundary):
        # 认证用户显式触发 → 允许（可创建 RunInvocation 起点）
        assert boundary.require_user_explicit(source="user_explicit", principal_type="user") is True

    def test_run_dag_auto_continue(self, boundary):
        # 同一用户 Run 内，Planner/Agent 按 DAG 自动继续（无需每次人工点击）
        assert boundary.run_auto_continue("running") is True


# ═══════════════════════════════════════════════════════
#  T3 Run 生命周期
# ═══════════════════════════════════════════════════════

class TestT3RunLifecycle:
    def test_no_auto_new_run_after_completion(self, boundary):
        # Run 完成后，DAG 自动继续关闭 → 禁止后台自动新建 Run
        assert boundary.run_auto_continue("completed") is False
        assert boundary.run_auto_continue("cancelled") is False
        assert boundary.run_auto_continue("failed") is False

    def test_no_detection_engine(self, boundary):
        # 无独立 Detection Engine / 告警自动 Agent 图
        assert not hasattr(boundary, "detection_engine")
        assert boundary.allow_auto_new_run() is False


# ═══════════════════════════════════════════════════════
#  T4 System Principal
# ═══════════════════════════════════════════════════════

class TestT4SystemPrincipal:
    def test_allowed_ops(self, boundary):
        assert boundary.system_principal_allowed("collect_evidence") is True
        assert boundary.system_principal_allowed("refresh_state") is True
        assert boundary.system_principal_allowed("health_check") is True

    def test_forbidden_ops(self, boundary):
        assert boundary.system_principal_allowed("create_run_invocation") is False
        assert boundary.system_principal_allowed("create_execution_scope") is False
        assert boundary.system_principal_allowed("approve_plan") is False

    def test_system_principal_cannot_create_run(self, boundary):
        # System Principal 无执行权：即使 source 看似 user，principal_type=system 仍拒绝
        with pytest.raises(ManualTriggerDenied):
            boundary.require_user_explicit(source="user_explicit", principal_type="system")
