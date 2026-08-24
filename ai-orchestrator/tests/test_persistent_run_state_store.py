"""P10 完整闭环 (Bugbot P0-1) — PersistentRunStateStore 生产接线。

验证：
- 未配置 query-api（QUERY_API_URL 空）→ 退化为纯内存 RunStateStore（保持既有行为）。
- 配置 query-api → 迁移走 PersistentRunRepository 远端提交优先，HTTP 失败不推进缓存。
"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import UUID

import pytest

import contracts
from control_plane_client import ControlPlaneClient, ControlPlaneError
from persistent_run_repository import PersistError, PersistentRunRepository, default_run_to_contract
from persistent_run_state_store import PersistentRunStateStore
from run_cache import RunCache
from run_persistence import RunPersistenceError, RunStateStore

RUN_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"


def _airun(status: str, version: int) -> dict:
    return {
        "run_id": RUN_ID, "request_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
        "tenant_id": TENANT, "principal": "91480408-9c2d-11f1-8271-bea176fe9f9f",
        "principal_type": "user", "scope_kind": "single_cluster",
        "primary_cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        "intent": "investigate", "action_mode": "read_only",
        "status": status, "state_version": version,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "updated_at": datetime.now(timezone.utc).isoformat(),
    }


class _FakeHTTP:
    def __init__(self, status=200, body=None):
        self.status = status
        self.body = body or {}
        self.calls = 0

    def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
        self.calls += 1
        import json
        return self.status, json.dumps(self.body).encode("utf-8")


def test_no_query_api_falls_back_to_in_memory():
    """未配置 query-api → 纯内存 RunStateStore（保持既有行为）。"""
    store = PersistentRunStateStore(repository=None)
    assert store.persisted is False
    r = store.create_run(
        run_id=UUID(RUN_ID), request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
        tenant_id=UUID(TENANT), intent="i", action_mode="read_only",
        principal_type="user", principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"))
    assert r.status == contracts.RunStatus.CREATED


def test_remote_commit_failure_does_not_advance_cache():
    """配置 query-api → 迁移远端提交；HTTP 500 不推进缓存（fail-closed，无双写）。"""
    http = _FakeHTTP(status=500, body={"error": "internal"})
    cache = RunCache()
    cache.put(default_run_to_contract(_airun("created", 0)))
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    store = PersistentRunStateStore(repository=repo)
    assert store.persisted is True
    with pytest.raises(ControlPlaneError):
        store.transition(run_id=UUID(RUN_ID), target="planning", tenant_id=UUID(TENANT))
    # 缓存仍是 created（远端未提交成功）。
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.CREATED


def test_remote_commit_success_advances_cache():
    http = _FakeHTTP(status=200, body={"run": _airun("planning", 1)})
    cache = RunCache()
    cache.put(default_run_to_contract(_airun("created", 0)))
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    store = PersistentRunStateStore(repository=repo)
    run = store.transition(run_id=UUID(RUN_ID), target="planning", tenant_id=UUID(TENANT))
    assert run.status == contracts.RunStatus.PLANNING
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.PLANNING


def test_create_via_query_api_not_supported():
    """Run 创建在 query-api 公共层；orchestrator persistent store 不支持本地 create。"""
    http = _FakeHTTP(status=200, body={})
    cache = RunCache()
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    store = PersistentRunStateStore(repository=repo)
    with pytest.raises(RunPersistenceError) as ex:
        store.create_run(run_id=UUID(RUN_ID), request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
                         tenant_id=UUID(TENANT), intent="i", action_mode="read_only",
                         principal_type="user", principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"))
    assert ex.value.error_code == "CREATE_VIA_QUERY_API"


def test_remote_get_cache_hit_returns_cache():
    """cache 命中 → 直接返回，不触发远端。"""
    http = _FakeHTTP(status=500, body={"error": "unavailable"})
    cache = RunCache()
    cache.put(default_run_to_contract(_airun("planning", 1)))
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    store = PersistentRunStateStore(repository=repo)
    run = store.get(UUID(RUN_ID))
    assert run.status == contracts.RunStatus.PLANNING
    assert http.calls == 0  # 未触发远端


def test_remote_get_cache_miss_fail_closed_on_remote_error():
    """A0-02（F-01）：remote 模式 cache-miss + 远端 refresh 失败 → fail-closed 抛错，
    绝不回退本地 fallback Run（Query API/MySQL 是唯一 SoT）。"""
    http = _FakeHTTP(status=503, body={"error": "unavailable"})
    cache = RunCache()  # 空 cache
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    # fallback（内存 store）里故意放一个 Run，验证不会用它当权威返回。
    fallback = RunStateStore()
    fallback.create_run(
        run_id=UUID(RUN_ID), request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
        tenant_id=UUID(TENANT), intent="i", action_mode="read_only",
        principal_type="user", principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"))
    store = PersistentRunStateStore(repository=repo, fallback=fallback)
    with pytest.raises(ControlPlaneError):
        store.get(UUID(RUN_ID))
    # 即使 fallback 有该 Run，也不应返回它作为权威（fail-closed 优先）。


def test_remote_get_cache_miss_unknown_tenant_fail_closed():
    """A0-02：cache/fallback 都无法确定 tenant → 拒绝 UUID(0) 猜测，fail-closed。"""
    http = _FakeHTTP(status=200, body={"run": _airun("planning", 1)})
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=RunCache())
    store = PersistentRunStateStore(repository=repo)  # 空 cache + 空 fallback
    with pytest.raises(PersistError) as ex:
        store.get(UUID(RUN_ID))
    assert ex.value.error_code == "RUN_TENANT_UNKNOWN"


def test_remote_get_cache_miss_with_tenant_success():
    """remote 模式 cache-miss + fallback 能提供 tenant → 用该 tenant 远端 refresh 成功。"""
    http = _FakeHTTP(status=200, body={"run": _airun("planning", 1)})
    cache = RunCache()
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    fallback = RunStateStore()
    fallback.create_run(
        run_id=UUID(RUN_ID), request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
        tenant_id=UUID(TENANT), intent="i", action_mode="read_only",
        principal_type="user", principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"))
    store = PersistentRunStateStore(repository=repo, fallback=fallback)
    run = store.get(UUID(RUN_ID))
    assert run.status == contracts.RunStatus.PLANNING
    assert http.calls >= 1  # 触发了远端 refresh
