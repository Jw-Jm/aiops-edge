"""RunCache：只缓存 query-api 已提交结果。

远端提交优先架构的组成部分：RunCache 只缓存 query-api 已提交的 Run；
HTTP 失败/冲突时不得推进缓存状态（不能"本地先成功、随后同步"形成双写不一致）。
"""
from __future__ import annotations

from uuid import UUID

import contracts
from run_persistence import RunPersistenceError


class RunCacheConflictError(RunPersistenceError):
    """缓存提交冲突：调用方必须先获得 query-api 远端成功，再 put_with_check。"""

    def __init__(self, run_id: UUID, expected: int, actual: int):
        super().__init__(
            "RUN_CACHE_CONFLICT",
            f"Run {run_id} 缓存版本冲突: expected {expected}, actual {actual}（远端未提交成功前不得推进）",
        )


class RunCache:
    """只缓存 query-api 已提交的 Run；冲突/失败不推进。"""

    def __init__(self) -> None:
        self._runs: dict[UUID, contracts.Run] = {}

    def get(self, run_id: UUID) -> contracts.Run | None:
        """返回缓存中的 Run；不存在返回 None（不抛错，查询时走远端）。"""
        rid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        return self._runs.get(rid)

    def put(self, run: contracts.Run) -> contracts.Run:
        """写入已提交的 Run（覆盖）。仅允许 query-api 已确认成功的提交调用。"""
        self._runs[run.run_id] = run
        return run

    def put_with_check(self, run: contracts.Run, expected_version: int) -> contracts.Run:
        """写入时校验 state_version 单调（防竞态/回退），失败不推进并抛 RunCacheConflictError。"""
        rid = run.run_id
        existing = self._runs.get(rid)
        if existing is not None and existing.state_version != expected_version:
            raise RunCacheConflictError(rid, expected_version, existing.state_version)
        if existing is not None and run.state_version != existing.state_version + 1:
            raise RunCacheConflictError(rid, existing.state_version + 1, run.state_version)
        self._runs[rid] = run
        return run

    def invalidate(self, run_id: UUID) -> None:
        rid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        self._runs.pop(rid, None)

    def all(self) -> list[contracts.Run]:
        return list(self._runs.values())

    def __len__(self) -> int:
        return len(self._runs)
