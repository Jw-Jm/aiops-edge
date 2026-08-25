import pytest

from tool_execution_context import ToolExecutionContext


def test_investigation_context_requires_lease_and_tool_identity():
    with pytest.raises(ValueError):
        ToolExecutionContext.from_mapping({"workload_kind": "investigation"}, tool_id="query_logs.v1", params={})

    ctx = ToolExecutionContext.from_mapping({
        "workload_kind": "investigation",
        "run_id": "22222222-2222-4222-8222-222222222222",
        "invocation_id": "99999999-9999-4999-8999-999999999999",
        "tenant_id": "55555555-5555-4555-8555-555555555555",
        "cluster_id": "66666666-6666-4666-8666-666666666666",
        "executor_id": "worker-1", "lease_epoch": 3, "lease_token": "token",
    }, tool_id="query_logs.v1", params={"service": "orders"})
    body = ctx.to_body()
    assert body["run_id"] == "22222222-2222-4222-8222-222222222222"
    assert body["workload_kind"] == "investigation"
    assert body["lease_epoch"] == 3
    assert body["idempotency_key"]
