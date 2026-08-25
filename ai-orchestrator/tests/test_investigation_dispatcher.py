import asyncio

import pytest

from investigation_dispatcher import AcceptedInvocation, InvestigationDispatcher


def invocation(run_id="run-a", invocation_id="inv-a"):
    return AcceptedInvocation(
        run_id=run_id,
        invocation_id=invocation_id,
        request_id="req-a",
        tenant_id="tenant-a",
        cluster_id="cluster-a",
        intent="diagnose",
        resource_id="service-a",
        service="service-a",
        message="diagnose service-a",
        action_mode="read_only",
    )


class BlockingRuntime:
    def __init__(self):
        self.started = asyncio.Event()
        self.release = asyncio.Event()
        self.finished = False
        self.status = None

    async def accept(self, item):
        self.status = "planning"
        return item

    async def execute(self, item):
        self.started.set()
        await self.release.wait()
        self.finished = True


class RecordingRuntime(BlockingRuntime):
    def __init__(self):
        super().__init__()
        self.accept_calls = 0

    async def accept(self, item):
        self.accept_calls += 1
        return await super().accept(item)


@pytest.mark.asyncio
async def test_accept_returns_before_worker_finishes():
    runtime = BlockingRuntime()
    dispatcher = InvestigationDispatcher(runtime, capacity=2)
    await dispatcher.start()
    try:
        result = await dispatcher.accept(invocation())
        assert result.accepted is True
        assert runtime.status == "planning"
        assert runtime.finished is False
        await asyncio.wait_for(runtime.started.wait(), timeout=1)
    finally:
        runtime.release.set()
        await dispatcher.stop()


@pytest.mark.asyncio
async def test_duplicate_invocation_is_queued_once():
    runtime = BlockingRuntime()
    dispatcher = InvestigationDispatcher(runtime, capacity=2)
    await dispatcher.start()
    try:
        first = await dispatcher.accept(invocation())
        second = await dispatcher.accept(invocation())
        assert first.accepted is True
        assert second.duplicate is True
        assert dispatcher.queued_count("inv-a") == 1
    finally:
        runtime.release.set()
        await dispatcher.stop()


@pytest.mark.asyncio
async def test_queue_saturation_does_not_claim_second_run():
    runtime = RecordingRuntime()
    dispatcher = InvestigationDispatcher(runtime, capacity=1)
    # Do not start a worker: the first item intentionally occupies the only slot.
    await dispatcher.accept(invocation("run-a", "inv-a"))
    with pytest.raises(asyncio.QueueFull):
        await dispatcher.accept(invocation("run-b", "inv-b"))
    assert runtime.accept_calls == 1
