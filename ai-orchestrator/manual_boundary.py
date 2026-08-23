"""P7.8 Manual AI Invocation Boundary — V9.3 Phase7 人工显式触发收敛。

核心原则：
- AI 调查入口统一收敛为人工显式触发。Alert/Event/Change 只能作为页面上下文/预填，
  不能自行创建 Run。
- 同一用户 Run 内 Planner/Agent 按 DAG 自动继续（无需每次人工点击）；Run 完成后
  后台不得自动新建 Run。
- System Principal 只能 collect evidence / refresh state / health check；
  不能 create RunInvocation / create execution scope / approve plan。
- 三身份分离：User→Intent、Service→Tool access、Run→Execution context；JWT(user) ≠ execution permission。
"""
from __future__ import annotations

# System Principal 允许的操作（其它一律禁止）
SYSTEM_PRINCIPAL_ALLOWED = frozenset({"collect_evidence", "refresh_state", "health_check"})

# 用户显式触发的来源；其它（alert/event/change/page_load/approved_system_event）不创建 Run
USER_EXPLICIT_SOURCES = frozenset({"user_explicit"})

# Run 内 DAG 自动继续仅在运行中允许
_AUTO_CONTINUE_RUN_STATUSES = frozenset({"running"})


class ManualTriggerDenied(ValueError):
    def __init__(self, message: str):
        self.error_code = "MANUAL_TRIGGER_REQUIRED"
        super().__init__(message)


class ManualBoundary:
    """人工触发 / System Principal / Run 生命周期边界守卫（内存 MVP）。"""

    def require_user_explicit(self, *, source: str, principal_type: str) -> bool:
        """仅认证用户显式触发允许创建 Run；其它来源 / system principal 拒绝。"""
        if principal_type != "user":
            raise ManualTriggerDenied("System Principal 不能创建 RunInvocation")
        if source not in USER_EXPLICIT_SOURCES:
            raise ManualTriggerDenied(f"AI 调查仅人工显式触发，禁止 {source} 自动创建 Run")
        return True

    def run_auto_continue(self, run_status: str) -> bool:
        """同一用户 Run 内 DAG 自动继续：仅 running 允许；完成后关闭。"""
        return run_status in _AUTO_CONTINUE_RUN_STATUSES

    def allow_auto_new_run(self) -> bool:
        """Run 结束后后台不得自动新建 Run（恒 False）。"""
        return False

    def system_principal_allowed(self, operation: str) -> bool:
        """System Principal 边界：collect evidence/refresh state/health check 允许。"""
        return operation in SYSTEM_PRINCIPAL_ALLOWED
