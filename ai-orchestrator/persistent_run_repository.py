"""V9.3 Phase 10 (P10 完整闭环 Plan B) — PersistentRunRepository（远端提交优先）。

否决 local-first 双写（"内存 mutation 成功 → 随后 HTTP 同步"会形成双写不一致）。
正确模型（§D2）：
    orchestrator 计算合法状态变化
      → 携带 command_id + expected_version 请求 query-api
      → query-api CAS + 持久化成功
      → 返回 committed Run
      → orchestrator 用返回结果更新 RunCache
HTTP 失败不推进缓存；响应丢失后用相同 command_id 重试，query-api 幂等返回首次提交结果。
"""
from __future__ import annotations

from typing import Any, Callable
from uuid import UUID

import contracts
from control_plane_client import ControlPlaneClient
from run_cache import RunCache
from run_persistence import RunPersistenceError
from run_state_machine import RunStateMachine


class PersistError(RunPersistenceError):
    """远端持久化失败（HTTP 失败/冲突），不得推进缓存。"""

    def __init__(self, code: str, message: str):
        super().__init__(code, message)


class PersistentRunRepository:
    """远端提交优先的 Run 仓储：CAS + command_id 幂等 + RunCache 缓存。"""

    def __init__(self, *, client: ControlPlaneClient, cache: RunCache,
                 run_to_contract: Callable[[dict], contracts.Run] | None = None) -> None:
        self._client = client
        self._cache = cache
        self._run_to_contract = run_to_contract or default_run_to_contract

    @property
    def cache(self) -> RunCache:
        """暴露 RunCache（供 PersistentRunStateStore 组合复用同一缓存）。"""
        return self._cache

    # ── transition（远端提交优先）────────────────────────────────────────
    def transition(self, *, run_id: UUID, target: str, expected_version: int,
                   tenant_id: UUID, command_id: str) -> contracts.Run:
        """校验合法迁移 → 远端 CAS 提交 → 成功才更新缓存。"""
        # 1) 本地状态机语义校验（不产生副作用）。
        current = self._current_status(run_id)
        RunStateMachine.validate_transition(current, target)
        # 2) 远端提交（CAS + 幂等 command_id）。
        resp = self._client.transition(
            run_id=str(run_id), target=target, expected_version=expected_version,
            tenant_id=str(tenant_id), command_id=command_id,
        )
        committed = self._extract_run(resp)
        # 3) 成功才更新缓存。
        self._cache.put_with_check(committed, expected_version=expected_version)
        return committed

    def cancel(self, *, run_id: UUID, expected_version: int, tenant_id: UUID,
               command_id: str) -> contracts.Run:
        current = self._current_status(run_id)
        if not RunStateMachine.can_cancel(current):
            raise RunPersistenceError("ILLEGAL_RUN_TRANSITION", f"Run {run_id} 当前状态不可 cancel")
        # A0-01（F-02）：把 expected_version + command_id 端到端传入 query-api（CAS + 幂等）。
        resp = self._client.cancel(
            run_id=str(run_id), tenant_id=str(tenant_id),
            expected_version=expected_version, command_id=command_id,
        )
        committed = self._extract_run(resp)
        self._cache.put_with_check(committed, expected_version=expected_version)
        return committed

    def refresh(self, *, run_id: UUID, tenant_id: UUID) -> contracts.Run:
        """从远端拉取权威 Run 并更新缓存（恢复/读取）。"""
        resp = self._client.get(run_id=str(run_id), tenant_id=str(tenant_id))
        committed = self._extract_run(resp)
        self._cache.put(committed)
        return committed

    def scan_unfinished(self, *, tenant_id: UUID) -> list[contracts.Run]:
        runs = self._client.list_unfinished(tenant_id=str(tenant_id))
        return [self._run_to_contract(r) for r in runs]

    def _current_status(self, run_id: UUID) -> str:
        cached = self._cache.get(run_id)
        if cached is not None:
            return cached.status.value
        # 缓存 miss：从远端拉取（回源）。
        # 此处不自动拉取以避免隐藏错误；调用方应保证先 refresh。无缓存且无远端时保守失败。
        raise PersistError("RUN_CACHE_MISS", f"Run {run_id} 不在缓存，需先 refresh 回源")

    def _extract_run(self, resp: dict) -> contracts.Run:
        r = resp.get("run")
        if not isinstance(r, dict):
            raise PersistError("INVALID_RESPONSE", "control-plane 响应缺少 run 字段")
        return self._run_to_contract(r)


def default_run_to_contract(r: dict) -> contracts.Run:
    """把 control-plane 返回的 AIRun JSON map 转为权威 contracts.Run。"""
    scope_raw = r.get("scope_kind", "single_cluster")
    scope = (contracts.RunScopeKind.MULTI_CLUSTER
             if scope_raw == "multi_cluster"
             else contracts.RunScopeKind.SINGLE_CLUSTER)
    status_val = r.get("status", "created")
    return contracts.Run(
        run_id=UUID(r["run_id"]),
        request_id=UUID(r["request_id"]),
        tenant_id=UUID(r["tenant_id"]),
        principal_type=r.get("principal_type", "user"),
        principal_id=UUID(r["principal"]),
        scope_kind=scope,
        primary_cluster_id=(UUID(r["primary_cluster_id"])
                            if r.get("primary_cluster_id") else None),
        intent=r.get("intent", ""),
        action_mode=r.get("action_mode", "read_only"),
        status=contracts.RunStatus(status_val),
        state_version=int(r.get("state_version", 0)),
        created_at=_parse_ts(r.get("created_at")),
        updated_at=_parse_ts(r.get("updated_at")),
    )


def _parse_ts(value: Any):
    from datetime import datetime
    if not value:
        return None
    if isinstance(value, datetime):
        return value
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except Exception:
        return None
