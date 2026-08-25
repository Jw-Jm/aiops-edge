from invocation_scope import InvocationScope
import tools


def _scope():
    return InvocationScope(
        principal_type="system", principal_id="33333333-3333-4333-8333-333333333333",
        session_id=None, tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666", request_id="11111111-1111-4111-8111-111111111111",
        source="orchestrator", run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999", workload_kind="investigation",
        executor_id="worker", lease_epoch=2, lease_token="token",
    )


def test_investigation_metrics_uses_toolrun_client(monkeypatch):
    calls = []

    def fake_query(*, tool_id, operation, params, context):
        calls.append((tool_id, operation, params, context))
        return {"data": [{"calls": 1, "errors": 0, "avg_ms": 4}]}

    monkeypatch.setattr(tools, "_internal_investigation_query", fake_query)
    result = tools.query_metrics("orders", cluster_id=_scope().cluster_id, request_context=_scope())
    assert calls and calls[0][0] == "query_metrics.v1"
    assert calls[0][2]["service"] == "orders"
    assert "avg_ms" in result
