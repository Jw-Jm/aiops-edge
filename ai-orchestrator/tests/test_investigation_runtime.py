import pytest

from investigation_dispatcher import AcceptedInvocation
from investigation_runtime import InvestigationRuntime


class Lease:
    def __init__(self):
        self.checked = 0
        self.closed = 0

    def check_active(self):
        self.checked += 1

    def commit(self, **kwargs):
        return {"status": kwargs["target"]}

    def close(self):
        self.closed += 1


class LeaseExecutor:
    def __init__(self):
        self.calls = []
        self.lease = Lease()

    def acquire(self, **kwargs):
        self.calls.append(kwargs)
        return self.lease


class ControlPlane:
    def __init__(self):
        self.status = "created"
        self.version = 0
        self.transitions = []

    def get(self, **kwargs):
        return {"run": {"status": self.status, "state_version": self.version}}

    def transition(self, *, run_id, target, expected_version, tenant_id, command_id):
        assert expected_version == self.version
        self.transitions.append((target, expected_version))
        self.status = target
        self.version += 1
        return {"run": {"status": target, "state_version": self.version}}


class Brain:
    async def investigate(self, item, lease):
        lease.check_active()
        return [{"type": "evidence", "id": "ev-1"}]


def item():
    return AcceptedInvocation(
        run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999",
        request_id="11111111-1111-4111-8111-111111111111",
        tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666",
        intent="diagnose", resource_id="service-a", service="service-a",
        message="diagnose", action_mode="read_only",
    )


@pytest.mark.asyncio
async def test_accept_claims_lease_and_advances_to_planning():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(control_plane=cp, lease_executor=leases, brain=Brain())
    work = await runtime.accept(item())
    assert cp.status == "planning"
    assert cp.transitions == [("planning", 0)]
    assert leases.calls[0]["run_id"] == item().run_id
    assert work.lease is leases.lease


@pytest.mark.asyncio
async def test_execute_progresses_investigating_verifying_and_success():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(control_plane=cp, lease_executor=leases, brain=Brain())
    work = await runtime.accept(item())
    await runtime.execute(work)
    assert [name for name, _ in cp.transitions] == ["planning", "investigating", "verifying"]
    assert leases.lease.checked >= 1
    assert leases.lease.closed == 1
