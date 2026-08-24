"""V9.3 Phase 10 (P10 完整闭环, Bugbot P0-1) — PersistentRunStateStore。

把 PersistentRunRepository（远端提交优先）组合进 RunStateStore，作为生产 Run 持久化
后端。设计依据 §D2：内存层只是缓存/状态机，query-api 是提交权威。

行为（env 门控，诚实边界）：
- 若配置了 QUERY_API_URL（真实 query-api），Run 的迁移经 PersistentRunRepository 远端
  提交优先持久化；HTTP 失败**不推进**内存缓存（fail-closed，无 local-first 双写）。
- 若未配置（当前 In-memory MVP 环境无真实 query-api），退化为纯内存 RunStateStore
  （保留既有行为，不破坏既有测试）。生产接线属后续真实环境 Integration Gate。
"""
from __future__ import annotations

from uuid import UUID

import contracts
from persistent_run_repository import PersistentRunRepository, PersistError
from run_cache import RunCache
from run_persistence import RunPersistenceError, RunStateStore


class PersistentRunStateStore:
    """组合 RunStateStore（状态机）+ PersistentRunRepository（远端提交优先）+ RunCache。"""

    def __init__(self, repository: PersistentRunRepository | None = None,
                 fallback: RunStateStore | None = None) -> None:
        self._repo = repository
        self._fallback = fallback if fallback is not None else RunStateStore()
        self._cache = repository.cache if repository is not None else RunCache()

    @property
    def persisted(self) -> bool:
        """是否启用远端持久化（配置了 query-api 后端）。"""
        return self._repo is not None

    # ── 对外接口：与 RunStateStore 对齐 ────────────────────────────────────
    def create_run(self, **kwargs) -> contracts.Run:
        if self._repo is None:
            return self._fallback.create_run(**kwargs)
        # 远端提交优先：向 query-api 创建 Run（run-invocations 携带 run_id/request_id）。
        # 由 PersistentRunRepository 扩展的 create 负责；这里走 fallback 语义由调用方
        # 传持久化好的 Run（repository 封装在 control-plane 层）。
        raise RunPersistenceError("CREATE_VIA_QUERY_API",
                                  "Run 创建在 query-api 公共层，orchestrator 仅刷新/迁移")

    def transition(self, *, run_id: UUID, target: str, tenant_id: UUID,
                   command_id: str | None = None) -> contracts.Run:
        if self._repo is None:
            return self._fallback.transition(run_id=run_id, target=target)
        expected = self._cache.get(run_id)
        exp = expected.state_version if expected is not None else 0
        return self._repo.transition(
            run_id=run_id, target=target, expected_version=exp,
            tenant_id=tenant_id, command_id=command_id or "",
        )

    def get(self, run_id: UUID) -> contracts.Run | None:
        if self._repo is None:
            return self._fallback.get(run_id)
        cached = self._cache.get(run_id)
        if cached is not None:
            return cached
        # 生产 remote 模式：远端 refresh 失败必须 fail-closed，绝不回退本地 fallback 当权威
        # Run（报告 27.3 / F-01：Query API/MySQL 是唯一 SoT，remote 404/403/503 都不得
        # 返回本地 RunStateStore）。
        tenant = self._tenant_for(run_id)
        if tenant is None:
            raise PersistError("RUN_TENANT_UNKNOWN",
                               f"Run {run_id} 无法确定 tenant，拒绝用 UUID(0) 猜测远端读取")
        return self._repo.refresh(run_id=run_id, tenant_id=tenant)

    def _tenant_for(self, run_id: UUID) -> UUID | None:
        """从本地 session/缓存状态解析 tenant（仅作读取所需 scope，不作权威 Run 来源）。
        无法确定 tenant 时返回 None（fail-closed），不再用 UUID(int=0) 猜测。"""
        cached = self._cache.get(run_id)
        if cached is not None:
            return cached.tenant_id
        try:
            r = self._fallback.get(run_id)
        except RunPersistenceError:
            return None
        if r is not None:
            return r.tenant_id
        return None

    def list(self, tenant_id: UUID | None = None) -> list[contracts.Run]:
        if self._repo is None:
            return self._fallback.list(tenant_id=tenant_id)
        if tenant_id is None:
            return []
        return self._repo.scan_unfinished(tenant_id=tenant_id)
