from invocation_scope import InvocationScope, ScopeViewSnapshot


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
