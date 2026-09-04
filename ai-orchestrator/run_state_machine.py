"""RunStateMachine：纯状态机 + 语义校验。

远端提交优先架构的组成部分：RunStateMachine 只做状态转换合法性校验与预算/终态判定，
不持有任何存储。持久化由 PersistentRunRepository（远端提交优先）负责，内存仅作
RunCache 缓存 query-api 已提交结果。
"""
from __future__ import annotations

from run_persistence import RunPersistenceError

# 终态：success/partial/failed/regressed/cancelled
TERMINAL: frozenset[str] = frozenset({"success", "partial", "failed", "regressed", "cancelled"})

# 可迁移（来自状态 → 目标状态集），与 contracts.RunStatus 对齐。
RUN_TRANSITIONS: dict[str, frozenset[str]] = {
    "created": frozenset({"planning", "cancelled"}),
    "planning": frozenset({"investigating", "awaiting_confirmation", "failed", "cancelled"}),
    "investigating": frozenset({"awaiting_confirmation", "awaiting_approval", "verifying", "failed", "cancelled"}),
    "awaiting_confirmation": frozenset({"investigating", "awaiting_approval", "cancelled"}),
    "awaiting_approval": frozenset({"executing", "cancelled", "failed"}),
    "executing": frozenset({"verifying", "success", "partial", "failed", "regressed", "cancelled"}),
    "verifying": frozenset({"success", "partial", "failed", "regressed", "cancelled"}),
}


class RunStateMachine:
    """P10.1 Run 状态机：纯状态转换 + 语义校验（无存储，无副作用）。"""

    @staticmethod
    def validate_transition(current: str, target: str) -> None:
        """状态迁移合法性（终态不可再迁；非法迁移拒绝）。"""
        if current in TERMINAL:
            raise RunPersistenceError("ILLEGAL_RUN_TRANSITION", f"终态 {current} 不可迁移")
        allowed = RUN_TRANSITIONS.get(current, frozenset())
        if target not in allowed:
            raise RunPersistenceError(
                "ILLEGAL_RUN_TRANSITION", f"非法 Run 迁移: {current} → {target}"
            )

    @staticmethod
    def is_terminal(status: str) -> bool:
        return status in TERMINAL

    @staticmethod
    def can_cancel(status: str) -> bool:
        """cancel 是显式 control action；终态不可 cancel。"""
        return status not in TERMINAL and "cancelled" in RUN_TRANSITIONS.get(status, frozenset())
