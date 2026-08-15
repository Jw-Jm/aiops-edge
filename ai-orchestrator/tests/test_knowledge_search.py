"""query_knowledge / search_playbooks 检索测试 (hermetic: 假嵌入器 + tmp_path)"""
import os
import tempfile

# 模块级 rag = RAGStore() 需要可写目录; 必须在 import rag 之前设置
os.environ["AIOPS_DATA_DIR"] = tempfile.mkdtemp(prefix="aiops_test_knowledge_")

import numpy as np
import pytest

from rag import RAGStore
from playbook_loader import load_playbooks, query_knowledge

FIXTURES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fixtures", "playbooks")


class FakeEmbedding:
    """确定性的字符 bigram 哈希嵌入, 避免加载 sentence-transformers 模型。"""

    def __init__(self):
        self._dim = 512

    def _vec(self, text):
        t = "".join((text or "").split())
        if not t:
            return np.zeros(self._dim, dtype=np.float32)
        if len(t) == 1:
            t = t * 2
        v = np.zeros(self._dim, dtype=np.float32)
        for i in range(len(t) - 1):
            g = t[i:i + 2]
            v[hash(g) % self._dim] += 1
        norm = np.linalg.norm(v)
        return v / norm if norm > 0 else v

    def __call__(self, input):
        texts = input if isinstance(input, (list, tuple)) else [input]
        return [self._vec(t) for t in texts]

    # chromadb 1.x 查询路径走 embed_query / 入库走 embed_documents
    def embed_query(self, input):
        return self._vec(input) if isinstance(input, str) else self.__call__(input)

    def embed_documents(self, input):
        return self.__call__(input)


@pytest.fixture()
def store(tmp_path, monkeypatch):
    monkeypatch.setattr("rag._get_ef", lambda: FakeEmbedding())
    s = RAGStore(persist_dir=str(tmp_path))
    load_playbooks(s, playbooks_dir=FIXTURES)
    return s


def test_search_playbooks_hits_oom_killed(store):
    results = store.search_playbooks("容器内存溢出", limit=5)
    assert results, "检索应有结果"
    scores = [r["score"] for r in results]
    assert scores == sorted(scores, reverse=True), "应按 score 降序"
    paths = [r["path"] for r in results]
    assert "diagnostics/oom-killed.md" in paths[:3], "OOMKilled playbook 应命中并靠前"


def test_search_playbooks_path_prefix_filter(store):
    results = store.search_playbooks("容器内存溢出", limit=10, path_prefix="diagnostics")
    assert results
    assert all(r["path"].startswith("diagnostics/") for r in results)
    # 无 path_prefix 时应能返回 diagnostics 之外的结果
    assert all(r["path"].startswith("diagnostics/")
               for r in store.search_playbooks("容器内存溢出", limit=10, path_prefix="diag"))


def test_search_playbooks_tags_filter(store):
    results = store.search_playbooks("容器内存溢出", limit=5, tags=["oom"])
    assert results
    assert all("oom" in r["tags"].split(",") for r in results)


def test_query_knowledge_shape_and_preview(store):
    out = query_knowledge("容器内存溢出", max_results=5, store=store)
    assert "items" in out and out["items"]
    it = out["items"][0]
    assert {"title", "path", "category", "tags", "score", "preview"}.issubset(it.keys())
    assert len(it["preview"]) <= 800, "preview 不超过 800 字符"
    assert it["source"] == "playbook"
    assert it["path"] == "diagnostics/oom-killed.md"


def test_query_knowledge_low_score_items_still_returned(store):
    # 无硬性 score 阈值: 不相关查询也返回 items (带 score 供 persona 判断)
    out = query_knowledge("完全不相关的词 qqqzzz", max_results=5, store=store)
    assert "items" in out
    assert out["items"], "低分条目默认仍返回"
    for it in out["items"]:
        assert "score" in it
        assert it["score"] <= 1.0


def test_query_knowledge_empty_query_returns_empty(store):
    out = query_knowledge("", store=store)
    assert out == {"items": []}


def test_query_knowledge_path_prefix_filter(store):
    out = query_knowledge("容器内存溢出", path_prefix="diagnostics", store=store)
    assert out["items"]
    assert all(it["path"].startswith("diagnostics/") for it in out["items"])


def test_query_knowledge_tags_filter(store):
    out = query_knowledge("容器内存溢出", tags="oom", store=store)
    assert out["items"]
    assert all("oom" in it["tags"].split(",") for it in out["items"])
