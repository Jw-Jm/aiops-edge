"""Knowledge routing must not bypass Query API for scoped requests."""

import json
import asyncio

import tools
from invocation_scope import InvocationScope


TENANT = "11111111-1111-4111-8111-111111111111"
CLUSTER = "22222222-2222-4222-8222-222222222222"
RUN = "33333333-3333-4333-8333-333333333333"
INVOCATION = "44444444-4444-4444-8444-444444444444"


def _investigation_context() -> InvocationScope:
    return InvocationScope(
        principal_type="system",
        principal_id="55555555-5555-4555-8555-555555555555",
        session_id=None,
        tenant_id=TENANT,
        cluster_id=CLUSTER,
        request_id="66666666-6666-4666-8666-666666666666",
        source="orchestrator",
        run_id=RUN,
        invocation_id=INVOCATION,
        workload_kind="investigation",
        capability="ai.investigate",
        executor_id="orchestrator:test",
        lease_epoch=3,
        lease_token="lease-token",
    )


def test_scoped_investigation_knowledge_uses_internal_query(monkeypatch):
    calls = []

    def fake_internal_query(**kwargs):
        calls.append(kwargs)
        return {"quality": "complete", "data": {"results": [{"document_id": "doc-1"}]}}

    monkeypatch.setattr(tools, "_internal_investigation_query", fake_internal_query)
    monkeypatch.setattr(
        "playbook_loader.query_knowledge",
        lambda *args, **kwargs: (_ for _ in ()).throw(AssertionError("local RAG bypass")),
    )

    result = json.loads(
        tools._query_knowledge(query="OOM", max_results=7, request_context=_investigation_context())
    )

    assert result["results"][0]["document_id"] == "doc-1"
    assert calls[0]["tool_id"] == "knowledge_search.v1"
    assert calls[0]["operation"] == "knowledge"
    assert calls[0]["params"] == {"query": "OOM", "top_k": 7}
    assert calls[0]["context"].tenant_id == TENANT
    assert calls[0]["context"].cluster_id == CLUSTER


def test_scoped_chat_knowledge_fails_closed_until_chat_audit():
    context = _investigation_context()
    context = InvocationScope(
        principal_type=context.principal_type,
        principal_id=context.principal_id,
        session_id=context.session_id,
        tenant_id=context.tenant_id,
        cluster_id=context.cluster_id,
        request_id=context.request_id,
        source=context.source,
        workload_kind="chat",
    )

    result = json.loads(tools._query_knowledge(query="OOM", request_context=context))

    assert result == {"error": "CHAT_KNOWLEDGE_AUDIT_REQUIRED"}


def test_scoped_legacy_filters_are_not_silently_dropped(monkeypatch):
    monkeypatch.setattr(
        tools,
        "_internal_investigation_query",
        lambda **_: (_ for _ in ()).throw(AssertionError("query should be rejected")),
    )

    result = json.loads(
        tools._query_knowledge(
            query="OOM", tags="kubernetes", request_context=_investigation_context()
        )
    )

    assert result == {"error": "KNOWLEDGE_FILTER_UNSUPPORTED"}


def test_scoped_memorize_does_not_write_local_chroma(monkeypatch):
    import orchestrator

    monkeypatch.setattr(
        orchestrator,
        "_case_quality_check",
        lambda *_: (_ for _ in ()).throw(AssertionError("local knowledge write")),
    )
    result = asyncio.run(
        orchestrator.node_memorize(
            {
                "verify_pass": True,
                "crewai_result": "A verified production case",
                "request_context": {
                    "tenant_id": TENANT,
                    "cluster_id": CLUSTER,
                },
            }
        )
    )

    assert "需经 Query API owner" in result["messages"][0]


def test_production_unscoped_knowledge_fails_closed(monkeypatch):
    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.setattr(
        "playbook_loader.query_knowledge",
        lambda *args, **kwargs: (_ for _ in ()).throw(AssertionError("local RAG in production")),
    )

    result = json.loads(tools._query_knowledge(query="OOM", request_context=None))

    assert result == {"error": "INVALID_CONTEXT"}
