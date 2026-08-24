"""Plan B Task B6 — PersistentRunRepository（远端提交优先 + command_id 幂等）。"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import UUID

import pytest

import contracts
from control_plane_client import ControlPlaneClient, ControlPlaneError
from persistent_run_repository import PersistentRunRepository, default_run_to_contract
from run_cache import RunCache
from run_persistence import RunPersistenceError
from run_state_machine import RunStateMachine

RUN_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
PRINCIPAL = "91480408-9c2d-11f1-8271-bea176fe9f9f"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"


def _airun(status: str, version: int) -> dict:
    return {
        "run_id": RUN_ID, "request_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
        "tenant_id": TENANT, "principal": PRINCIPAL, "principal_type": "user",
        "scope_kind": "single_cluster", "primary_cluster_id": CLUSTER,
        "intent": "investigate", "action_mode": "read_only",
        "status": status, "state_version": version,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "updated_at": datetime.now(timezone.utc).isoformat(),
    }


class FakeHTTP:
    """可配置的 fake http transport（成功/失败/超时/响应丢失重试）。"""

    def __init__(self):
        self.calls = 0
        self.status = 200
        self.body: dict = {}
        self.error = None

    def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
        self.calls += 1
        if self.error:
            raise self.error
        import json
        return self.status, json.dumps(self.body).encode("utf-8")


def _client(http: FakeHTTP) -> ControlPlaneClient:
    return ControlPlaneClient(http=http)


def _cache_with(run: contracts.Run) -> RunCache:
    cache = RunCache()
    cache.put(run)
    return cache


def _contract(status: str, version: int) -> contracts.Run:
    return default_run_to_contract(_airun(status, version))


def test_commit_success_updates_cache():
    http = FakeHTTP()
    http.body = {"run": _airun("planning", 1)}
    cache = RunCache()
    cache.put(_contract("created", 0))
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    committed = repo.transition(
        run_id=UUID(RUN_ID), target="planning", expected_version=0,
        tenant_id=UUID(TENANT), command_id="c1")
    assert committed.status == contracts.RunStatus.PLANNING
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.PLANNING
    assert http.calls == 1


def test_commit_http_failure_does_not_update_cache():
    http = FakeHTTP()
    http.status = 503
    http.body = {"error": "unavailable"}
    cache = RunCache()
    cache.put(_contract("created", 0))
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    with pytest.raises(ControlPlaneError):
        repo.transition(
            run_id=UUID(RUN_ID), target="planning", expected_version=0,
            tenant_id=UUID(TENANT), command_id="c1")
    # 缓存不推进（仍是 created）
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.CREATED


def test_commit_state_machine_rejects_illegal():
    http = FakeHTTP()
    cache = RunCache()
    cache.put(_contract("created", 0))
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    with pytest.raises(RunPersistenceError) as ex:
        repo.transition(
            run_id=UUID(RUN_ID), target="executing", expected_version=0,
            tenant_id=UUID(TENANT), command_id="c1")
    assert ex.value.error_code == "ILLEGAL_RUN_TRANSITION"
    assert http.calls == 0  # 本地校验失败，不发远端


def test_commit_cas_conflict_raises():
    http = FakeHTTP()
    http.status = 409
    http.body = {"error": "RUN_STATE_CONFLICT"}
    cache = RunCache()
    cache.put(_contract("created", 0))
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    with pytest.raises(ControlPlaneError) as ex:
        repo.transition(
            run_id=UUID(RUN_ID), target="planning", expected_version=5,
            tenant_id=UUID(TENANT), command_id="c1")
    assert ex.value.kind == "RUN_STATE_CONFLICT"
    assert cache.get(UUID(RUN_ID)).status == contracts.RunStatus.CREATED


def test_refresh_updates_cache_from_remote():
    http = FakeHTTP()
    http.body = {"run": _airun("planning", 1)}
    cache = RunCache()
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    run = repo.refresh(run_id=UUID(RUN_ID), tenant_id=UUID(TENANT))
    assert run.status == contracts.RunStatus.PLANNING
    assert cache.get(UUID(RUN_ID)) is not None


def test_scan_unfinished():
    http = FakeHTTP()
    http.body = {"runs": [_airun("planning", 1), _airun("created", 0)], "total": 2}
    repo = PersistentRunRepository(client=_client(http), cache=RunCache())
    runs = repo.scan_unfinished(tenant_id=UUID(TENANT))
    assert len(runs) == 2


def test_cancel_passes_expected_version_and_command_id():
    """A0-01（F-02）：PersistentRunRepository.cancel 必须把 expected_version + command_id
    端到端传给 ControlPlaneClient（不再 POST 空 body / 丢参数）。"""
    class RecordingHTTP:
        def __init__(self):
            self.calls = []
        def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
            import json
            self.calls.append({
                "path": path, "method": method,
                "body": json.loads(data.decode("utf-8")) if data else {},
            })
            return 200, json.dumps({"run": _airun("cancelled", 1)}).encode("utf-8")

    http = RecordingHTTP()
    cache = RunCache()
    cache.put(_contract("planning", 0))
    repo = PersistentRunRepository(client=_client(http), cache=cache)
    committed = repo.cancel(
        run_id=UUID(RUN_ID), expected_version=0, tenant_id=UUID(TENANT), command_id="cancel-1")
    assert committed.status == contracts.RunStatus.CANCELLED
    assert http.calls, "cancel 必须发起远端请求"
    call = http.calls[0]
    assert call["path"].endswith("/cancel"), call["path"]
    # 端到端传参：expected_version + command_id 必须出现在 body。
    assert call["body"]["expected_version"] == 0
    assert call["body"]["command_id"] == "cancel-1"


def test_cancel_local_state_machine_rejects_terminal():
    """本地状态机校验：终态 Run 不可 cancel，不发远端请求。"""
    class NoCallHTTP:
        def __call__(self, path, *, context_claims, method="POST", data=None, headers=None):
            raise AssertionError("terminal cancel should not call remote")
    cache = RunCache()
    cache.put(_contract("success", 2))
    repo = PersistentRunRepository(client=_client(NoCallHTTP()), cache=cache)
    with pytest.raises(RunPersistenceError):
        repo.cancel(run_id=UUID(RUN_ID), expected_version=2,
                    tenant_id=UUID(TENANT), command_id="c1")


def test_state_machine_alignment():
    # PersistentRunRepository 与 RunStateMachine 共享状态机语义。
    RunStateMachine.validate_transition("created", "planning")
    with pytest.raises(RunPersistenceError):
        RunStateMachine.validate_transition("success", "planning")
