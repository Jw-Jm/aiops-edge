"""V9.3 Phase 10 — Run Persistence / SSE / Recovery 测试（P10.1-P10.8 + Gate 10）。"""
import uuid

import pytest

import contracts
from run_persistence import (
    RunEventStore,
    RunPersistenceError,
    RunStateConflictError,
    RunStateStore,
)
from sse_stream import SSEStream, SSEStreamError

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
REQ = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
TENANT = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
CLUSTER = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
PRINCIPAL = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"


def _store():
    return RunStateStore()


def _create(store=None, run_id=RUN, request_id=REQ):
    s = store or _store()
    return s, s.create_run(
        run_id=run_id, request_id=request_id, tenant_id=TENANT,
        intent="latency spike", action_mode="read_only",
        principal_type="user", principal_id=PRINCIPAL,
        primary_cluster_id=CLUSTER,
    )


# ── P10.1 Run 状态机 + optimistic CAS ───────────────────────────────────

def test_create_run_initial_status_and_version():
    _, r = _create()
    assert r.status == contracts.RunStatus.CREATED
    assert r.state_version == 0
    assert str(r.tenant_id) == TENANT


def test_valid_transition_ok():
    s, r = _create()
    r2 = s.transition(r.run_id, "planning", expected_version=r.state_version)
    assert r2.status == contracts.RunStatus.PLANNING
    assert r2.state_version == 1


def test_illegal_transition_rejected():
    s, r = _create()
    with pytest.raises(RunPersistenceError) as ex:
        s.transition(r.run_id, "success")  # created → success 非法
    assert ex.value.error_code == "ILLEGAL_RUN_TRANSITION"


def test_terminal_state_cannot_migrate():
    s, r = _create()
    r = s.transition(r.run_id, "planning", expected_version=0)
    r = s.transition(r.run_id, "failed", expected_version=1)
    with pytest.raises(RunPersistenceError):
        s.transition(r.run_id, "planning")  # failed 是终态


def test_cas_conflict_409():
    s, r = _create()
    s.transition(r.run_id, "planning", expected_version=0)
    # 用过期 expected_version → 409 RUN_STATE_CONFLICT
    with pytest.raises(RunStateConflictError) as ex:
        s.transition(r.run_id, "investigating", expected_version=0)
    assert ex.value.error_code == "RUN_STATE_CONFLICT"


# ── P10.7 Idempotency ───────────────────────────────────────────────────

def test_duplicate_request_id_idempotent():
    s, r = _create()
    r2 = s.create_run(
        run_id=RUN, request_id=REQ, tenant_id=TENANT, intent="x", action_mode="read_only",
        principal_type="user", principal_id=PRINCIPAL, primary_cluster_id=CLUSTER,
    )
    assert r2.run_id == r.run_id  # 幂等返回既有 Run


def test_same_request_id_different_run_rejected():
    s, r = _create()
    with pytest.raises(RunPersistenceError) as ex:
        s.create_run(
            run_id=uuid.uuid4(), request_id=REQ, tenant_id=TENANT, intent="x",
            action_mode="read_only", principal_type="user", principal_id=PRINCIPAL,
        )
    assert ex.value.error_code == "DUPLICATE_REQUEST_ID"


# ── P10.8 Cancel（显式 control，不自动）────────────────────────────────

def test_cancel_is_explicit():
    s, r = _create()
    r2 = s.cancel(r.run_id, expected_version=r.state_version)
    assert r2.status == contracts.RunStatus.CANCELLED
    assert r2.finished_at is not None


def test_cancel_terminal_rejected():
    s, r = _create()
    r = s.transition(r.run_id, "planning", expected_version=0)
    r = s.transition(r.run_id, "failed", expected_version=1)
    with pytest.raises(RunPersistenceError):
        s.cancel(r.run_id)


def test_sse_disconnect_does_not_cancel():
    """P10.8：SSE disconnect / timeout 不能自动等价 cancel。"""
    events = RunEventStore()
    s, r = _create()
    stream = SSEStream(events, s)
    stream.disconnect(r.run_id)  # 只移除订阅，不触 Run 状态
    assert s.get(r.run_id).status == contracts.RunStatus.CREATED  # 未 cancel


# ── P10.3 Event Persistence（单调 sequence）────────────────────────────

def test_event_sequence_monotonic():
    events = RunEventStore()
    e1 = events.append(run_id=RUN, event="plan_created", tenant_id=TENANT)
    e2 = events.append(run_id=RUN, event="step_completed", tenant_id=TENANT, cluster_id=CLUSTER)
    assert e1.sequence == 1
    assert e2.sequence == 2
    assert str(e2.cluster_id) == CLUSTER


def test_sequence_per_run_independent():
    events = RunEventStore()
    events.append(run_id=RUN, event="a", tenant_id=TENANT)
    events.append(run_id=uuid.uuid4(), event="b", tenant_id=TENANT)
    assert events.last_sequence(RUN) == 1


# ── P10.5 Replay ────────────────────────────────────────────────────────

def test_replay_after_sequence():
    events = RunEventStore()
    for i in range(3):
        events.append(run_id=RUN, event=f"e{i}", tenant_id=TENANT)
    replay = events.replay(RUN, after_sequence=1)
    assert [e.sequence for e in replay] == [2, 3]
    assert [e.event for e in replay] == ["e1", "e2"]


def test_sse_stream_replay_authorization():
    events = RunEventStore()
    s, r = _create()
    events.append(run_id=RUN, event="e0", tenant_id=TENANT)
    events.append(run_id=RUN, event="e1", tenant_id=TENANT)
    stream = SSEStream(events, s)
    # 未授权 reconnect → 拒绝
    with pytest.raises(SSEStreamError) as ex:
        stream.subscribe(run_id=RUN, tenant_id=TENANT, authorized=False)
    assert ex.value.error_code == "SSE_UNAUTHORIZED"
    # 授权 + after_sequence=1 → 只重放 e1
    replay = stream.subscribe(run_id=RUN, tenant_id=TENANT, after_sequence=1, authorized=True)
    assert [e.event for e in replay] == ["e1"]


def test_sse_retention_exceeded_rejects():
    events = RunEventStore()
    s, r = _create()
    stream = SSEStream(events, s)
    with pytest.raises(SSEStreamError) as ex:
        stream.subscribe(run_id=RUN, tenant_id=TENANT, after_sequence=99999, authorized=True)
    assert ex.value.error_code == "SSE_RETENTION_EXCEEDED"


def test_sse_rejects_wrong_tenant_for_run():
    """审计 P1-2：SSE 订阅必须校验 tenant_id 与 Run 归属一致（跨租户拒绝，fail-closed）。"""
    events = RunEventStore()
    s, r = _create()  # Run 的 tenant = TENANT
    stream = SSEStream(events, s)
    other_tenant = "ffffffff-ffff-4fff-8fff-ffffffffffff"
    with pytest.raises(SSEStreamError) as ex:
        stream.subscribe(run_id=RUN, tenant_id=other_tenant, authorized=True)
    assert ex.value.error_code == "SSE_TENANT_MISMATCH"


# ── P10.6 Recovery ──────────────────────────────────────────────────────

def test_scan_unfinished_and_restore():
    s, r = _create()
    r2 = s.transition(r.run_id, "planning", expected_version=0)
    unfinished = s.scan_unfinished()
    assert len(unfinished) == 1
    # recovery re-entry 幂等：restore 已存在 run → 拒绝
    s2 = RunStateStore()
    s2._restore(r2)
    with pytest.raises(RunPersistenceError) as ex:
        s2._restore(r2)
    assert ex.value.error_code == "RECOVERY_REENTRY"


def test_recovery_restores_relationships():
    """Gate 10：Run relationships survive restart。"""
    s, r = _create()
    # 模拟重启：新 store，恢复同一 Run（request_id/tenant/状态关系保留）
    s2 = RunStateStore()
    s2._restore(r)
    restored = s2.get(r.run_id)
    assert str(restored.request_id) == REQ
    assert str(restored.tenant_id) == TENANT
    assert restored.status == contracts.RunStatus.CREATED


# ── Gate 10 断言 ───────────────────────────────────────────────────────

def test_gate10_no_parallel_incident_persistence():
    """Gate 10：本 Phase 不新增 Incident/Detection runtime tables。"""
    import run_persistence
    assert not hasattr(run_persistence, "IncidentStore")
    assert not hasattr(run_persistence, "DetectionStore")


def test_gate10_cas_conflict_deterministic():
    s, r = _create()
    s.transition(r.run_id, "planning", expected_version=0)
    with pytest.raises(RunStateConflictError):
        s.transition(r.run_id, "investigating", expected_version=0)
    with pytest.raises(RunStateConflictError):
        s.transition(r.run_id, "failed", expected_version=0)  # 都基于过期版本 → 稳定 409
