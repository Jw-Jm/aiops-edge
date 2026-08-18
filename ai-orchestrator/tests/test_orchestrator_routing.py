import asyncio

import pytest


pytest.importorskip("langgraph")

import orchestrator
from orchestrator import _explicit_tool_route, _is_info_query


def test_explicit_k8sgpt_request_wins_over_info_query_markers():
    question = "请用 k8sgpt 诊断当前集群有哪些问题"

    assert _explicit_tool_route(question) == "k8sgpt_diagnose"
    assert not _is_info_query(question)


def test_explicit_knowledge_search_request_wins_over_info_query_markers():
    question = "请参考知识库，列出 Go 服务 OOM 的处理经验"

    assert _explicit_tool_route(question) == "query_knowledge"
    assert not _is_info_query(question)


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
