import asyncio

import pytest


pytest.importorskip("langgraph")

import orchestrator
from orchestrator import _explicit_tool_route, _explicit_tool_routes, _is_info_query


def test_explicit_k8sgpt_request_wins_over_info_query_markers():
    question = "请用 k8sgpt 诊断当前集群有哪些问题"

    assert _explicit_tool_route(question) == "k8sgpt_diagnose"
    assert not _is_info_query(question)


def test_explicit_knowledge_search_request_wins_over_info_query_markers():
    question = "请参考知识库，列出 Go 服务 OOM 的处理经验"

    assert _explicit_tool_route(question) == "query_knowledge"
    assert not _is_info_query(question)


def test_combined_request_routes_to_both_explicit_tools():
    question = "请用 k8sgpt 诊断并参考知识库分析当前集群问题"

    assert _explicit_tool_routes(question) == ["k8sgpt_diagnose", "query_knowledge"]
    # The compatibility helper remains deterministic for callers that only
    # support one route; execution paths consume the complete list above.
    assert _explicit_tool_route(question) == "k8sgpt_diagnose"


def test_combined_request_executes_both_tools(monkeypatch):
    """组合显式请求不能因首个路由命中而跳过知识库工具。"""
    import tools

    calls = []
    monkeypatch.setattr(orchestrator, "get_service_list", lambda **_: "[]")
    monkeypatch.setattr(orchestrator, "get_infrastructure", lambda: "")
    monkeypatch.setattr(orchestrator, "_collect_alerts", lambda: "")
    monkeypatch.setattr(orchestrator, "query_logs", lambda *args, **kwargs: "")
    monkeypatch.setattr(orchestrator, "k8sgpt_diagnose",
                        lambda: calls.append("k8sgpt") or "diagnostic evidence")
    monkeypatch.setattr(tools, "_query_knowledge",
                        lambda **_: calls.append("knowledge") or '{"title":"OOM playbook"}')

    question = "请用 k8sgpt 诊断并参考知识库分析当前集群问题"
    collected = asyncio.run(orchestrator.node_collect({
        "intent": "diagnosis", "service": "", "user_message": question,
        "cluster_id": "all", "llm_config": None,
    }))
    ragged = asyncio.run(orchestrator.node_rag({
        "user_message": question, "service": "", **collected,
    }))

    assert calls == ["k8sgpt", "knowledge"]
    assert collected["k8sgpt_raw"] == "diagnostic evidence"
    assert "OOM playbook" in ragged["similar_cases"]


def test_english_knowledge_search_request_uses_query_knowledge(monkeypatch):
    question = "use knowledge search for Go service OOM"
    assert _explicit_tool_route(question) == "query_knowledge"

    import tools
    monkeypatch.setattr(tools, "_query_knowledge", lambda **_: '{"title":"OOM playbook"}')
    result = asyncio.run(orchestrator.node_rag({"user_message": question, "service": ""}))
    assert "OOM playbook" in result["similar_cases"]


def test_plain_service_list_remains_lightweight_info_query():
    assert _explicit_tool_route("当前有哪些服务在运行") is None
    assert _is_info_query("当前有哪些服务在运行")


def test_explicit_k8sgpt_request_invokes_tool_and_keeps_result(monkeypatch):
    calls = []
    monkeypatch.setattr(orchestrator, "get_service_list", lambda **_: "[]")
    monkeypatch.setattr(orchestrator, "get_infrastructure", lambda: "")
    monkeypatch.setattr(orchestrator, "_collect_alerts", lambda: "")
    monkeypatch.setattr(orchestrator, "query_logs", lambda *args, **kwargs: "")
    monkeypatch.setattr(orchestrator, "k8sgpt_diagnose", lambda: calls.append(True) or "K8sGPT result")

    state = {
        "intent": "diagnosis",
        "service": "",
        "user_message": "请用 k8sgpt 诊断当前集群有哪些问题",
        "cluster_id": "all",
        "llm_config": None,
    }
    result = asyncio.run(orchestrator.node_collect(state))

    assert calls == [True]
    assert result["k8sgpt_raw"] == "K8sGPT result"


def test_unavailable_k8sgpt_is_error_and_cannot_be_health_evidence(monkeypatch):
    """工具无结果时不能被映射成成功或“未发现集群问题”。"""
    monkeypatch.setattr(orchestrator, "get_service_list", lambda **_: "[]")
    monkeypatch.setattr(orchestrator, "get_infrastructure", lambda: "K8s 基础设施数据不可用")
    monkeypatch.setattr(orchestrator, "_collect_alerts", lambda: "")
    monkeypatch.setattr(orchestrator, "query_logs", lambda *args, **kwargs: "")
    monkeypatch.setattr(orchestrator, "k8sgpt_diagnose", lambda: "未发现集群问题")

    state = {
        "intent": "diagnosis",
        "service": "",
        "user_message": "请用 k8sgpt 诊断当前集群有哪些问题",
        "cluster_id": "all",
        "llm_config": None,
    }
    result = asyncio.run(orchestrator.node_collect(state))

    assert result.get("k8sgpt_raw", "") == ""
    assert result["k8sgpt_error"]
    report = asyncio.run(orchestrator.node_summarize({**state, **result}))
    assert "未发现集群问题" not in report["final_response"]
    assert "无法" in report["final_response"] or "不可用" in report["final_response"]


def test_explicit_knowledge_request_exposes_matching_tool_result(monkeypatch):
    import tools

    monkeypatch.setattr(tools, "_query_knowledge", lambda **_: '{"title":"OOM playbook"}')
    result = asyncio.run(orchestrator.node_rag({
        "user_message": "请参考知识库，列出 Go 服务 OOM 的处理经验",
        "service": "",
    }))

    assert "OOM playbook" in result["similar_cases"]


def test_explicit_k8sgpt_result_is_retained_for_inspection_report():
    result = asyncio.run(orchestrator.node_summarize({
        "intent": "inspection",
        "light_query": False,
        "user_message": "请用 k8sgpt 诊断当前集群有哪些问题",
        "k8sgpt_raw": "K8sGPT found a failing deployment",
        "crewai_result": "基础设施巡检完成",
    }))
    assert "K8sGPT found a failing deployment" in result["final_response"]


def test_explicit_knowledge_tool_failure_is_not_relabelled_as_success(monkeypatch):
    import tools

    def fail_knowledge(**_):
        raise RuntimeError("knowledge backend unavailable")

    monkeypatch.setattr(tools, "_query_knowledge", fail_knowledge)
    result = asyncio.run(orchestrator.node_rag({
        "user_message": "use knowledge search for OOM",
        "service": "",
    }))
    assert result.get("knowledge_tool_error")
    assert result["similar_cases"] == ""


def test_empty_explicit_knowledge_result_is_an_error(monkeypatch):
    import tools

    monkeypatch.setattr(tools, "_query_knowledge", lambda **_: "")
    result = asyncio.run(orchestrator.node_rag({
        "user_message": "use knowledge search for OOM",
        "service": "",
    }))

    assert result.get("knowledge_tool_error")
    assert result["similar_cases"] == ""


def test_combined_k8sgpt_and_knowledge_request_keeps_both_evidence_sections():
    result = asyncio.run(orchestrator.node_summarize({
        "intent": "diagnosis",
        "user_message": "请使用 k8sgpt 诊断并检索知识库中的 OOMKilled 手册",
        "k8sgpt_raw": "K8sGPT evidence",
        "similar_cases": "## 知识库检索结果\nOOMKilled playbook",
        "crewai_result": "确定性诊断",
    }))
    assert "K8sGPT evidence" in result["final_response"]
    assert "OOMKilled playbook" in result["final_response"]


def test_k8sgpt_unavailable_is_visible_in_final_report():
    result = asyncio.run(orchestrator.node_summarize({
        "intent": "diagnosis",
        "user_message": "请用 k8sgpt 诊断当前集群",
        "k8sgpt_error": "K8sGPT unavailable: no verifiable diagnostic result",
        "crewai_result": "无法可靠判断",
    }))
    assert "K8sGPT unavailable" in result["final_response"]


def test_stream_marks_empty_k8sgpt_and_rag_results_unavailable(monkeypatch):
    """SSE 工具事件必须保留不可用语义，不能把空结果映射为 completed。"""

    class FakeGraph:
        async def astream(self, initial, config):
            yield {"collect": {"k8sgpt_raw": "", "k8sgpt_error": ""}}
            yield {"rag": {"similar_cases": "", "knowledge_tool_error": ""}}

    brain = orchestrator.BrainOrchestrator.__new__(orchestrator.BrainOrchestrator)
    brain.chat_graph = FakeGraph()
    brain.graph = brain.chat_graph
    brain.llm_config = None
    monkeypatch.setattr(brain, "_ensure_async_checkpointer", lambda: asyncio.sleep(0))
    monkeypatch.setattr(brain, "_detect_service", lambda _: "")
    monkeypatch.setattr(brain, "get_session_state", lambda _: None)

    async def collect_events():
        return [event async for event in brain.stream_sync(
            "diagnosis", "", "请用 k8sgpt 诊断并参考知识库分析当前集群问题",
            thread_id="test-stream", mode="chat")]

    events = asyncio.run(collect_events())
    k8s_end = next(event for event in events
                   if event.get("type") == "tool_end" and event.get("name") == "k8sgpt_diagnose")
    rag_end = next(event for event in events
                   if event.get("type") == "tool_end" and event.get("name") == "query_knowledge")
    assert k8s_end["status"] == "unavailable"
    assert rag_end["status"] == "unavailable"
