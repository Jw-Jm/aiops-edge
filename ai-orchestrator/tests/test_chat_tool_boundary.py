from __future__ import annotations

import asyncio
import json
import urllib.error

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


def test_legacy_query_transport_error_is_stable_and_non_sensitive(monkeypatch):
    import tools

    monkeypatch.setattr(
        tools,
        "signed_query_api_request",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            urllib.error.URLError("https://query.internal?token=super-secret")
        ),
    )
    result = tools._get_json("https://query.internal/api/v1/services")
    assert result == {"error": "QUERY_TRANSPORT_ERROR"}
    assert "super-secret" not in json.dumps(result)


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


def test_node_collect_k8sgpt_exception_is_stable_and_non_sensitive(monkeypatch):
    import orchestrator

    monkeypatch.setattr(
        orchestrator,
        "k8sgpt_diagnose",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("provider api_key=super-secret host=10.0.0.7")
        ),
    )
    result = asyncio.run(orchestrator.node_collect({
        "intent": "diagnosis",
        "service": "checkout",
        "user_message": "请用 k8sgpt 诊断 checkout",
        "llm_config": None,
        "request_context": None,
    }))
    assert result["k8sgpt_error"] == "K8sGPT error: K8SGPT_FAILED"
    assert "super-secret" not in result["k8sgpt_error"]
    assert "10.0.0.7" not in result["k8sgpt_error"]


def test_node_rag_exception_is_stable_and_non_sensitive(monkeypatch):
    import orchestrator
    import tools

    monkeypatch.setattr(
        tools,
        "_query_knowledge",
        lambda **_kwargs: (_ for _ in ()).throw(
            RuntimeError("mysql password=hunter2 host=db.internal")
        ),
    )
    result = asyncio.run(orchestrator.node_rag({
        "intent": "diagnosis",
        "service": "checkout",
        "user_message": "query_knowledge OOM",
        "request_context": None,
    }))
    assert result["knowledge_tool_error"] == "知识库检索失败: KNOWLEDGE_QUERY_FAILED"
    assert "hunter2" not in result["knowledge_tool_error"]
    assert "db.internal" not in result["knowledge_tool_error"]


def test_node_rca_exception_is_stable_and_non_sensitive(monkeypatch):
    import orchestrator

    monkeypatch.setattr(
        orchestrator,
        "full_rca_analysis",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("query https://query.internal?token=super-secret failed")
        ),
    )
    result = asyncio.run(orchestrator.node_rca({
        "service": "checkout",
        "cluster_id": CLUSTER,
        "request_context": None,
    }))
    assert result["rca_mode"] == "error"
    assert result["messages"][0].endswith("RCA: 失败 (RCA_FAILED)")
    assert "super-secret" not in result["messages"][0]
    assert "query.internal" not in result["messages"][0]


def test_node_verify_exception_is_stable_and_non_sensitive(monkeypatch):
    import orchestrator

    async def no_wait(_seconds):
        return None

    monkeypatch.setattr(orchestrator.asyncio, "sleep", no_wait)
    monkeypatch.setattr(
        orchestrator,
        "query_metrics",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("clickhouse password=super-secret host=10.0.0.7")
        ),
    )
    result = asyncio.run(orchestrator.node_verify({
        "service": "checkout",
        "cluster_id": CLUSTER,
        "before_metrics": "P50延迟=100ms",
        "request_context": None,
    }))
    assert result["verify_error_code"] == "VERIFICATION_SOURCE_UNAVAILABLE"
    assert "super-secret" not in result["messages"][0]
    assert "10.0.0.7" not in result["messages"][0]


def test_run_dag_exception_is_stable_and_non_sensitive(monkeypatch):
    import orchestrator

    class FailingGraph:
        async def ainvoke(self, *_args, **_kwargs):
            raise RuntimeError("provider token=super-secret host=10.0.0.7")

    async def no_checkpointer():
        return None

    brain = orchestrator.BrainOrchestrator.__new__(orchestrator.BrainOrchestrator)
    brain.graph = FailingGraph()
    brain.chat_graph = brain.graph
    brain.llm_config = None
    brain._ensure_async_checkpointer = no_checkpointer
    scope = _chat_scope()
    result = asyncio.run(brain._run_dag(
        "diagnosis", "checkout", "检查 checkout", request_context=scope,
    ))
    assert result == {
        "final_response": "[DAG 执行异常: BRAIN_ERROR]",
        "error": "BRAIN_ERROR",
    }
    assert "super-secret" not in json.dumps(result)
    assert "10.0.0.7" not in json.dumps(result)


def test_execute_suggestion_exception_is_stable_and_non_sensitive(monkeypatch):
    import subprocess
    import orchestrator
    import shell_policy

    class AllowPolicy:
        def check(self, _command):
            return None

        def check_shell_metachars(self, _command):
            return None

        def is_whitelisted_for_execute(self, _command):
            return True, "readonly"

        def check_extra_blacklist(self, _command):
            return None

    monkeypatch.setattr(shell_policy, "ShellPolicy", AllowPolicy)
    monkeypatch.setattr(
        subprocess,
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("provider token=super-secret host=10.0.0.7")
        ),
    )
    monkeypatch.setattr(orchestrator, "_audit_log", lambda *_args, **_kwargs: None)
    brain = orchestrator.BrainOrchestrator.__new__(orchestrator.BrainOrchestrator)
    result = brain.execute_suggestion("checkout", "kubectl get pods", task_id="task-1")
    assert result == "执行异常: EXECUTION_FAILED"
    assert "super-secret" not in result
    assert "10.0.0.7" not in result


def test_execute_shell_exception_is_stable_and_non_sensitive(monkeypatch):
    import subprocess
    import shell_policy
    import tools

    class AllowPolicy:
        def check(self, _command):
            return None

        def check_shell_metachars(self, _command):
            return None

        def is_whitelisted_for_execute(self, _command):
            return True, "readonly"

        def check_extra_blacklist(self, _command):
            return None

    monkeypatch.setattr(shell_policy, "ShellPolicy", AllowPolicy)
    monkeypatch.setattr(
        subprocess,
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("mysql password=super-secret host=10.0.0.7")
        ),
    )
    result = tools.execute_shell("kubectl get pods")
    assert result == "执行失败: EXECUTION_FAILED"
    assert "super-secret" not in result
    assert "10.0.0.7" not in result


def test_k8sgpt_exception_is_stable_and_non_sensitive(monkeypatch):
    import tools

    monkeypatch.setattr(tools, "_fetch_llm_config_for_k8sgpt", lambda: {
        "api_key": "super-secret", "base_url": "https://llm.internal", "model": "test"
    })
    monkeypatch.setattr(
        tools,
        "_run_k8sgpt",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            RuntimeError("provider host=10.0.0.7 token=super-secret")
        ),
    )
    result = tools.k8sgpt_diagnose("observability")
    assert result == "K8sGPT unavailable: K8SGPT_FAILED"
    assert "super-secret" not in result
    assert "10.0.0.7" not in result


def test_mcp_tool_exception_is_stable_and_non_sensitive(monkeypatch):
    import mcp_server

    server = mcp_server.MCPServer()
    server.tools["query_metrics"]["handler"] = lambda **_kwargs: (_ for _ in ()).throw(
        RuntimeError("mysql password=super-secret host=10.0.0.7")
    )
    result = json.loads(server.call_tool("query_metrics", {"service": "checkout"}))
    assert result == {"error": "MCP_TOOL_FAILED"}
    assert "super-secret" not in json.dumps(result)
    assert "10.0.0.7" not in json.dumps(result)
