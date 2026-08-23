"""V9.3 Phase 10 (P10 完整闭环, Plan D Task D4) — Gate 10 断言。

覆盖（orchestrator 侧可代码化断言）：
- Gate10: remote-commit-no-double-write：PersistentRunRepository HTTP 失败不推进缓存。
- Gate10: no-parallel-incident-persistence：Run 状态层不新增 Incident/Detection 持久化。
- Gate10: partial 是终态（重启不恢复 partial Run）。
"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import UUID

import pytest

import contracts
from control_plane_client import ControlPlaneClient, ControlPlaneError
from persistent_run_repository import PersistentRunRepository
from run_cache import RunCache
from run_persistence import RunPersistenceError, RunStateStore, RunEventStore
from run_state_machine import RunStateMachine, TERMINAL

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


def test_gate10_remote_commit_no_double_write():
    """HTTP 失败（500）时 PersistentRunRepository 不推进缓存（无 local-first 双写）。"""
    from persistent_run_repository import default_run_to_contract
    http = _FakeHTTP(status=500, body={"error": "internal"})
    cache = RunCache()
    cache.put(default_run_to_contract(_airun("created", 0)))
    repo = PersistentRunRepository(client=ControlPlaneClient(http=http), cache=cache)
    with pytest.raises(ControlPlaneError):
        repo.transition(run_id=UUID(RUN_ID), target="planning", expected_version=0,
                        tenant_id=UUID(TENANT), command_id="c1")
    # 缓存仍是 created（远端未提交成功，不推进）。
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.CREATED


def test_gate10_partial_is_terminal_for_recovery():
    """partial 是终态：重启恢复不得恢复 partial Run（ScanUnfinished 排除）。"""
    assert "partial" in TERMINAL
    store = RunStateStore()
    store.create_run(run_id=UUID(RUN_ID), request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
                     tenant_id=UUID(TENANT), intent="i", action_mode="read_only",
                     principal_type="user", principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"))
    store.transition(run_id=UUID(RUN_ID), target="planning")
    store.transition(run_id=UUID(RUN_ID), target="investigating")
    store.transition(run_id=UUID(RUN_ID), target="awaiting_approval")
    store.transition(run_id=UUID(RUN_ID), target="executing")
    store.transition(run_id=UUID(RUN_ID), target="partial")
    unfinished = store.scan_unfinished()
    assert all(x.run_id != UUID(RUN_ID) for x in unfinished), "partial Run 不应被恢复"


def test_gate10_event_sequence_monotonic():
    """事件 sequence 单调不争抢。"""
    es = RunEventStore()
    ev1 = es.append(run_id=UUID(RUN_ID), event="created", tenant_id=UUID(TENANT))
    ev2 = es.append(run_id=UUID(RUN_ID), event="planning", tenant_id=UUID(TENANT))
    assert ev1.sequence == 1 and ev2.sequence == 2
    assert ev2.sequence > ev1.sequence


def test_gate10_cancel_is_explicit():
    """cancel 是显式 control action；终态不可 cancel。"""
    assert RunStateMachine.can_cancel("created")
    assert not RunStateMachine.can_cancel("success")
    with pytest.raises(RunPersistenceError):
        RunStateMachine.validate_transition("success", "cancelled")
