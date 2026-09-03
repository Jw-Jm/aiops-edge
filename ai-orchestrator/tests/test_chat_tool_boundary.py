from __future__ import annotations

import json

from invocation_scope import InvocationScope, ScopeViewSnapshot


TENANT = "11111111-1111-4111-8111-111111111111"
CLUSTER = "22222222-2222-4222-8222-222222222222"
PRINCIPAL = "33333333-3333-4333-8333-333333333333"
SESSION = "44444444-4444-4444-8444-444444444444"
CHAT_SESSION = "55555555-5555-4555-8555-555555555555"
CHAT_TURN = "66666666-6666-4666-8666-666666666666"


def _chat_scope() -> InvocationScope:
    return InvocationScope(
        principal_type="user", principal_id=PRINCIPAL, session_id=SESSION,
        tenant_id=TENANT, cluster_id=CLUSTER,
        request_id="77777777-7777-4777-8777-777777777777", source="query-api",
        workload_kind="chat", chat_session_id=CHAT_SESSION, chat_turn_id=CHAT_TURN,
    )


def test_chat_read_tools_use_internal_audited_query(monkeypatch):
    import tools

    calls = []

    def fake_internal(**kwargs):
        calls.append(kwargs)
        return {"quality": "complete", "data": {"points": [], "total": 0}}

    monkeypatch.setattr(tools, "_internal_chat_query", fake_internal)
    monkeypatch.setattr(tools, "_get_json", lambda *_args, **_kwargs: (_ for _ in ()).throw(
        AssertionError("chat must not use public query-api fallback")
    ))
    result = tools.query_metrics("checkout", cluster_id=CLUSTER, request_context=_chat_scope())
    assert json.loads(result)["quality"] == "complete"
    assert calls[0]["tool_id"] == "query_metrics.v1"
    context = calls[0]["context"]
    assert context.workload_kind == "chat"
    assert context.chat_session_id == CHAT_SESSION
    assert context.chat_turn_id == CHAT_TURN


def test_chat_query_exception_does_not_echo_sensitive_exception(monkeypatch):
    import tools

    monkeypatch.setattr(
        tools, "_internal_chat_query",
        lambda **_: (_ for _ in ()).throw(RuntimeError("provider api_key=super-secret host=10.0.0.7")),
    )
    result = tools.query_metrics("checkout", cluster_id=CLUSTER, request_context=_chat_scope())
    assert result == "查询失败: QUERY_FAILED"
    assert "super-secret" not in result
    assert "10.0.0.7" not in result


def test_chat_scope_projection_keeps_audit_identity():
    projection = ScopeViewSnapshot.to_projection(_chat_scope())
    assert projection["chat_session_id"] == CHAT_SESSION
    assert projection["chat_turn_id"] == CHAT_TURN
    restored = ScopeViewSnapshot.from_projection(projection)
    assert restored.chat_session_id == CHAT_SESSION
    assert restored.chat_turn_id == CHAT_TURN


def test_legacy_knowledge_graph_tool_is_closed_in_production(monkeypatch):
    import kg_tools

    monkeypatch.setenv("AIOPS_ENV", "production")
    result = kg_tools.kg_evidence_tool("checkout", CLUSTER)
    assert result == "KNOWLEDGE_GRAPH_INVESTIGATION_REQUIRED"
