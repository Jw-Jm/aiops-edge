import pytest

from investigation_dispatcher import AcceptedInvocation, InvestigationDispatcher


class RecoveryRuntime:
    def __init__(self):
        self.accepted = []

    async def accept(self, item):
        self.accepted.append(item.invocation_id)
        return item

    async def execute(self, item):
        return None


def _item():
    return AcceptedInvocation(
        run_id="22222222-2222-4222-8222-222222222222", invocation_id="99999999-9999-4999-8999-999999999999",
        request_id="11111111-1111-4111-8111-111111111111", tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666", intent="diagnose", resource_id="orders",
        service="orders", message="diagnose", action_mode="read_only",
    )


@pytest.mark.asyncio
async def test_startup_recovery_requeues_unfinished_invocations():
    runtime = RecoveryRuntime()
    dispatcher = InvestigationDispatcher(runtime)
    await dispatcher.start()
    try:
        await dispatcher.recover([_item()])
        assert runtime.accepted == [_item().invocation_id]
    finally:
        await dispatcher.stop()
