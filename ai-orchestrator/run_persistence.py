"""V9.3 Phase 10 — Run Persistence / Event Persistence / Recovery（In-memory MVP + TDD）。

合同 §七十六：
- P10.1 Run 状态机 + optimistic CAS：只扩展既有 Run persistence；所有状态迁移校验合法性；
  `state_version` optimistic CAS；冲突返回明确 409（RUN_STATE_CONFLICT），禁止 last-write-wins。
- P10.2 Persistence Boundary：orchestrator semantic owner；query-api persistence owner。
- P10.3 Event Persistence：business SSE event 持久化到 ai_run_events 后才允许可靠 replay；
  sequence 单调，不允许多 owner 争抢 sequence。
- P10.6 Recovery：orchestrator restart 扫描未终结 Run→恢复 runtime state→重建可继续步骤。
- P10.7 Idempotency：覆盖 duplicate request_id、run creation、control command、event append、recovery re-entry。
- P10.8 Cancel：cancel 是显式 control action；SSE disconnect/browser close/timeout 都不能自动等价 cancel。

边界：In-memory MVP（不接真实 MySQL/SSE 代理）；不新增 Incident/Detection runtime tables。
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Any
from uuid import UUID

import contracts


class RunPersistenceError(ValueError):
    """Run 持久化错误（稳定错误码）。"""

    def __init__(self, code: str, message: str):
        self.error_code = code
        super().__init__(message)


class RunStateConflictError(RunPersistenceError):
    """optimistic CAS 冲突（409 RUN_STATE_CONFLICT，禁止 last-write-wins）。"""

    def __init__(self, message: str):
        super().__init__("RUN_STATE_CONFLICT", message)


# ── P10.1 Run 状态机（合法迁移表）────────────────────────────────────────
# 终态：success/partial/failed/regressed/cancelled
_TERMINAL = frozenset({"success", "partial", "failed", "regressed", "cancelled"})
# 可迁移（来自状态 → 目标状态集）
_RUN_TRANSITIONS = {
    "created": {"planning", "cancelled"},
    "planning": {"investigating", "awaiting_confirmation", "failed", "cancelled"},
    "investigating": {"awaiting_confirmation", "awaiting_approval", "failed", "cancelled"},
    "awaiting_confirmation": {"investigating", "awaiting_approval", "cancelled"},
    "awaiting_approval": {"executing", "cancelled", "failed"},
    "executing": {"verifying", "success", "partial", "failed", "regressed", "cancelled"},
    "verifying": {"success", "partial", "failed", "regressed", "cancelled"},
}


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _validate_transition(current: str, target: str) -> None:
    """P10.1 状态迁移合法性（终态不可再迁；非法迁移拒绝）。"""
    if current in _TERMINAL:
        raise RunPersistenceError("ILLEGAL_RUN_TRANSITION", f"终态 {current} 不可迁移")
    allowed = _RUN_TRANSITIONS.get(current, set())
    if target not in allowed:
        raise RunPersistenceError(
            "ILLEGAL_RUN_TRANSITION", f"非法 Run 迁移: {current} → {target}"
        )


class RunStateStore:
    """P10.1/P10.2/P10.6 — Run 状态机 + optimistic CAS（In-memory，semantic owner=orchestrator）。

    持权威 contracts.Run；state_version 乐观 CAS；冲突抛 RunStateConflictError（409）。
    """

    def __init__(self) -> None:
        self._runs: dict[UUID, contracts.Run] = {}
        self._request_id_index: dict[UUID, UUID] = {}  # request_id → run_id（幂等）

    def get(self, run_id: UUID) -> contracts.Run:
        if run_id not in self._runs:
            raise RunPersistenceError("RUN_NOT_FOUND", f"Run {run_id} 不存在")
        return self._runs[run_id]

    def create_run(
        self, *, run_id: UUID, request_id: UUID, tenant_id: UUID,
        intent: str, action_mode: str,
        principal_type: str, principal_id: UUID,
        primary_cluster_id: UUID | None = None,
        expected_version: int | None = None,
    ) -> contracts.Run:
        """P10.7 Idempotency：相同 request_id 幂等返回既有 Run；不同 run_id 相同 request_id 拒绝。"""
        req_uuid = request_id if isinstance(request_id, UUID) else UUID(str(request_id))
        run_uuid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        existing = self._request_id_index.get(req_uuid)
        if existing is not None:
            if existing == run_uuid:
                return self._runs[existing]
            raise RunPersistenceError("DUPLICATE_REQUEST_ID", f"request_id {request_id} 已被 {existing} 占用")
        if run_uuid in self._runs:
            raise RunPersistenceError("DUPLICATE_RUN_ID", f"Run {run_id} 已存在")
        scope_kind = (
            contracts.RunScopeKind.SINGLE_CLUSTER if primary_cluster_id
            else contracts.RunScopeKind.MULTI_CLUSTER
        )
        r = contracts.Run(
            run_id=run_id, request_id=request_id, tenant_id=tenant_id,
            principal_type=principal_type, principal_id=principal_id,
            scope_kind=scope_kind,
            primary_cluster_id=primary_cluster_id,
            intent=intent, action_mode=action_mode,
            status=contracts.RunStatus.CREATED,
            state_version=0,
            created_at=_now(), updated_at=_now(),
        )
        # 统一用权威 coerce 后的 UUID 做 key（防字符串/UUID 不一致）
        self._runs[r.run_id] = r
        self._request_id_index[r.request_id] = r.run_id
        return r

    def transition(
        self, run_id: UUID, target: str, *, expected_version: int | None = None,
    ) -> contracts.Run:
        """P10.1 optimistic CAS 迁移。expected_version 不符 → RUN_STATE_CONFLICT（409）。"""
        r = self.get(run_id)
        if expected_version is not None and r.state_version != expected_version:
            raise RunStateConflictError(
                f"Run {run_id} state_version 冲突: expected {expected_version}, actual {r.state_version}"
            )
        current = r.status.value
        _validate_transition(current, target)
        new = r.model_copy(update={
            "status": contracts.RunStatus(target),
            "state_version": r.state_version + 1,
            "updated_at": _now(),
        })
        self._runs[run_id] = new
        return new

    # ── P10.8 Cancel（显式 control action）──────────────────────────────
    def cancel(self, run_id: UUID, *, expected_version: int | None = None) -> contracts.Run:
        """cancel 是显式 control action；disconnect/timeout 不能自动等价 cancel（调用方决定）。"""
        r = self.get(run_id)
        if expected_version is not None and r.state_version != expected_version:
            raise RunStateConflictError(
                f"Run {run_id} state_version 冲突: expected {expected_version}, actual {r.state_version}"
            )
        if r.status.value in _TERMINAL:
            raise RunPersistenceError("ILLEGAL_RUN_TRANSITION", f"终态 {r.status.value} 不可 cancel")
        _validate_transition(r.status.value, "cancelled")
        new = r.model_copy(update={
            "status": contracts.RunStatus.CANCELLED,
            "state_version": r.state_version + 1,
            "updated_at": _now(), "finished_at": _now(),
        })
        self._runs[run_id] = new
        return new

    # ── P10.6 Recovery ──────────────────────────────────────────────────
    def scan_unfinished(self) -> list[contracts.Run]:
        """重启后扫描未终结 Run（非终态）→ 待恢复。"""
        return [r for r in self._runs.values() if r.status.value not in _TERMINAL]

    def all_runs(self) -> list[contracts.Run]:
        """列出全部 Run（P12 前端调查中心数据源）。"""
        return list(self._runs.values())

    def _restore(self, run: contracts.Run) -> None:
        """recovery re-entry 幂等：同 run_id 已存在则拒绝（P10.7）。"""
        if run.run_id in self._runs:
            raise RunPersistenceError("RECOVERY_REENTRY", f"Run {run.run_id} 恢复重入冲突")
        self._runs[run.run_id] = run
        self._request_id_index.setdefault(run.request_id, run.run_id)


class RunEventStore:
    """P10.3 — business SSE 事件持久化（ai_run_events 语义，单调 sequence）。

    sequence 单调递增（每 run 独立），不允许多 owner 争抢；持久化后才允许 replay。
    """

    def __init__(self) -> None:
        self._events: dict[UUID, list[contracts.SSEEvent]] = {}
        self._sequence: dict[UUID, int] = {}

    def append(self, *, run_id: UUID, event: str, tenant_id: UUID,
               cluster_id: UUID | None = None,
               payload: dict[str, Any] | None = None) -> contracts.SSEEvent:
        """P10.3 单调 sequence（禁多 owner 争抢）。"""
        run_uuid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        seq = self._sequence.get(run_uuid, 0) + 1
        self._sequence[run_uuid] = seq
        ev = contracts.SSEEvent(
            event=event, run_id=run_uuid, sequence=seq,
            timestamp=_now(), tenant_id=tenant_id, cluster_id=cluster_id,
            payload=dict(payload or {}),
        )
        self._events.setdefault(run_uuid, []).append(ev)
        return ev

    # ── P10.5 Replay ────────────────────────────────────────────────────
    def replay(self, run_id: UUID, after_sequence: int = 0) -> list[contracts.SSEEvent]:
        """返回 sequence > after_sequence 的事件（Last-Event-ID）。"""
        run_uuid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        evs = self._events.get(run_uuid, [])
        return [e for e in evs if e.sequence > after_sequence]

    def last_sequence(self, run_id: UUID) -> int:
        run_uuid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        return self._sequence.get(run_uuid, 0)
