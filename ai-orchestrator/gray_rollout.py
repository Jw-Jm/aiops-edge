"""PE.6 灰度执行阶段机 — V9.3 Execution Production Enablement。

将真实执行渐进放量（PE.6）：
Stage0 dry-run → Stage1 单资源 → Stage2 受限范围 → Stage3 全量。
每阶段闸门：allowed → 推进；denied → 终止（停止，进入回滚）；failed → 终止。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Dict, Optional

STAGES = ["stage0_dry_run", "stage1_single", "stage2_restricted", "stage3_full"]
ROLLOUT_STATUSES = {"pending", "running", "completed", "denied", "failed"}


class RolloutDenied(ValueError):
    def __init__(self, message: str):
        self.error_code = "ROLLOUT_DENIED"
        super().__init__(message)


@dataclass
class GrayRolloutState:
    rollout_id: str
    contract_id: str
    current_stage: str = STAGES[0]
    status: str = "running"


class GrayRollout:
    """内存灰度执行阶段机（MVP）。"""

    def __init__(self) -> None:
        self._store: Dict[str, GrayRolloutState] = {}

    def start(self, contract_id: str) -> GrayRolloutState:
        state = GrayRolloutState(
            rollout_id=str(uuid.uuid4()),
            contract_id=contract_id,
            current_stage=STAGES[0],
            status="running",
        )
        self._store[state.rollout_id] = state
        return state

    def advance(self, rollout_id: str, *, allowed: bool) -> GrayRolloutState:
        state = self._store.get(rollout_id)
        if state is None:
            raise RolloutDenied(f"rollout 不存在: {rollout_id}")
        if state.status != "running":
            raise RolloutDenied(f"rollout 已终止（{state.status}），不能推进")
        if not allowed:
            state.status = "denied"
            raise RolloutDenied(f"灰度闸门失败，停止于 {state.current_stage}（进入回滚）")
        idx = STAGES.index(state.current_stage)
        if idx == len(STAGES) - 1:
            state.status = "completed"
        else:
            state.current_stage = STAGES[idx + 1]
        return state

    def mark_failed(self, rollout_id: str) -> GrayRolloutState:
        state = self._store.get(rollout_id)
        if state is None:
            raise RolloutDenied(f"rollout 不存在: {rollout_id}")
        state.status = "failed"
        return state

    def is_completed(self, rollout_id: str) -> bool:
        state = self._store.get(rollout_id)
        return state is not None and state.status == "completed"

    def get(self, rollout_id: str) -> Optional[GrayRolloutState]:
        return self._store.get(rollout_id)
