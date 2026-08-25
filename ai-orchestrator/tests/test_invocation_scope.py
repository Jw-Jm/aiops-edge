from invocation_scope import InvocationScope, ScopeViewSnapshot
from tool_execution_context import ToolExecutionContext


def test_investigation_workload_survives_checkpoint_projection():
    scope = InvocationScope(
        principal_type="system",
        principal_id="33333333-3333-4333-8333-333333333333",
        session_id=None,
        tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666",
        request_id="11111111-1111-4111-8111-111111111111",
        source="orchestrator",
        run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999",
        workload_kind="investigation",
        capability="ai.investigate",
    )
    restored = ScopeViewSnapshot.from_projection(ScopeViewSnapshot.to_projection(scope))
    assert restored.workload_kind == "investigation"
    assert restored.capability == "ai.investigate"


def test_checkpoint_projection_preserves_invocation_and_non_secret_lease_identity():
    scope = InvocationScope(
        principal_type="system",
        principal_id="33333333-3333-4333-8333-333333333333",
        session_id=None,
        tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666",
        request_id="11111111-1111-4111-8111-111111111111",
        source="orchestrator",
        run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999",
        workload_kind="investigation",
        capability="ai.investigate",
    ).bind_lease(executor_id="orchestrator:worker", lease_epoch=7, lease_token="secret-token")

    projection = ScopeViewSnapshot.to_projection(scope)
    restored = ScopeViewSnapshot.from_projection(projection)

    assert projection["invocation_id"] == scope.invocation_id
    assert projection["executor_id"] == scope.executor_id
    assert projection["lease_epoch"] == scope.lease_epoch
    assert "lease_token" not in projection
    assert restored.invocation_id == scope.invocation_id
    assert restored.executor_id == scope.executor_id
    assert restored.lease_epoch == scope.lease_epoch

    context = ToolExecutionContext.from_mapping(
        {
            "workload_kind": restored.workload_kind,
            "run_id": restored.run_id,
            "invocation_id": restored.invocation_id,
            "tenant_id": restored.tenant_id,
            "cluster_id": restored.cluster_id,
            "executor_id": restored.executor_id,
            "lease_epoch": restored.lease_epoch,
            "lease_token": "secret-token",
        },
        tool_id="query_metrics.v1",
        params={"service": "orders"},
    )
    assert context.lease_epoch == 7
