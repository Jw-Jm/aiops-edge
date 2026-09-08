"""Regression tests for the local RAG bootstrap and fail-closed write path."""

import os
import tempfile

os.environ.setdefault("AIOPS_DATA_DIR", tempfile.mkdtemp(prefix="aiops_rag_contract_"))

import pytest

import rag as rag_module
from db_agents import KnowledgeStore
from rag import RAGStore


def test_add_case_returns_empty_when_collection_is_unavailable(monkeypatch, tmp_path):
    store = RAGStore(persist_dir=str(tmp_path))
    monkeypatch.setattr(store, "_ensure_init", lambda: False)

    result = store.add_case({
        "case_id": "case-unavailable",
        "symptom": "集群事件读取失败",
        "root_cause": "权限不足",
        "plan": "补充读取权限",
    })

    assert result == ""


def test_knowledge_store_does_not_mark_failed_bootstrap_ready(monkeypatch, tmp_path):
    expected = str(tmp_path / "ops-cases")
    monkeypatch.setenv("AIOPS_DATA_DIR", str(tmp_path))
    monkeypatch.setattr(KnowledgeStore, "_rag_ready_paths", set())
    monkeypatch.setattr(rag_module.RAGStore, "ensure_collections", lambda **_: None)

    KnowledgeStore._rag()

    assert expected not in KnowledgeStore._rag_ready_paths


def test_embedding_function_mismatch_reuses_existing_collection():
    expected = object()

    class ExistingCollectionClient:
        def get_collection(self, _name, embedding_function=None):
            if embedding_function is not None:
                raise ValueError("Embedding function conflict")
            return expected

    assert RAGStore._get_collection_compatible(
        ExistingCollectionClient(), "ops_cases", object()) is expected


@pytest.mark.asyncio
async def test_knowledge_case_returns_503_when_rag_write_is_unavailable(monkeypatch):
    import main
    from fastapi import HTTPException

    class UnavailableRAG:
        def _ensure_init(self):
            return True

        def add_case(self, _case):
            return ""

    unavailable = UnavailableRAG()
    monkeypatch.setattr(rag_module, "rag", unavailable)
    monkeypatch.setattr(KnowledgeStore, "_rag", staticmethod(lambda: unavailable))
    monkeypatch.setattr(main, "_case_quality_check", lambda _state: (True, ""))
    monkeypatch.setenv("GRAPH_BACKEND", "hugegraph")

    with pytest.raises(HTTPException) as exc_info:
        await main.add_knowledge_case({
            "service": "payments",
            "symptom": "支付服务持续返回超时错误",
            "root_cause": "下游连接池耗尽",
            "plan": "扩容连接池并观察错误率",
        })

    assert exc_info.value.status_code == 503
