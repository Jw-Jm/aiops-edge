"""Plan B Task B5 — RunStateMachine + RunCache（P10 完整闭环）。"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import UUID

import pytest

import contracts
from run_cache import RunCache, RunCacheConflictError
from run_persistence import RunPersistenceError
from run_state_machine import RunStateMachine, TERMINAL


def _run(status: str, version: int = 0) -> contracts.Run:
    return contracts.Run(
        run_id=UUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
        request_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
        tenant_id=UUID("7ed01afc-cc79-4ecd-8767-a2befa6168ad"),
        principal_type="user",
        principal_id=UUID("91480408-9c2d-11f1-8271-bea176fe9f9f"),
        scope_kind=contracts.RunScopeKind.SINGLE_CLUSTER,
        primary_cluster_id=UUID("91771a6e-9c2d-11f1-8271-bea176fe9f9f"),
        intent="investigate",
        action_mode="read_only",
        status=contracts.RunStatus(status),
        state_version=version,
        created_at=datetime.now(timezone.utc),
        updated_at=datetime.now(timezone.utc),
    )


def test_state_machine_rejects_terminal_migration():
    with pytest.raises(RunPersistenceError) as ex:
        RunStateMachine.validate_transition("success", "planning")
    assert ex.value.error_code == "ILLEGAL_RUN_TRANSITION"


def test_state_machine_rejects_illegal_transition():
    with pytest.raises(RunPersistenceError) as ex:
        RunStateMachine.validate_transition("created", "executing")
    assert ex.value.error_code == "ILLEGAL_RUN_TRANSITION"


def test_state_machine_accepts_legal_transition():
    RunStateMachine.validate_transition("created", "planning")
    RunStateMachine.validate_transition("executing", "partial")


def test_read_only_investigation_can_enter_verifying():
    RunStateMachine.validate_transition("investigating", "verifying")


def test_state_machine_terminal_and_cancel():
    assert RunStateMachine.is_terminal("partial")
    assert not RunStateMachine.is_terminal("created")
    assert RunStateMachine.can_cancel("created")
    assert not RunStateMachine.can_cancel("success")
    assert "partial" in TERMINAL


def test_run_cache_put_and_get():
    cache = RunCache()
    r = _run("created", 0)
    cache.put(r)
    assert cache.get(r.run_id).status == contracts.RunStatus.CREATED
    assert len(cache) == 1


def test_run_cache_put_with_check_ok():
    cache = RunCache()
    cache.put(_run("created", 0))
    updated = _run("planning", 1)
    cache.put_with_check(updated, expected_version=0)
    assert cache.get(updated.run_id).state_version == 1


def test_run_cache_conflict_does_not_advance():
    cache = RunCache()
    cache.put(_run("created", 0))
    # 模拟远端未提交成功的冲突提交（expected 不匹配）
    with pytest.raises(RunCacheConflictError):
        cache.put_with_check(_run("planning", 1), expected_version=5)
    # 缓存保持原状态（不推进）
    assert cache.get(cache.all()[0].run_id).status == contracts.RunStatus.CREATED


def test_run_cache_invalidate():
    cache = RunCache()
    r = _run("created", 0)
    cache.put(r)
    cache.invalidate(r.run_id)
    assert cache.get(r.run_id) is None
