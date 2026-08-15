"""playbook_loader 加载器测试 (hermetic: 假嵌入器 + tmp_path 持久化)"""
import os
import tempfile

# 模块级 rag = RAGStore() 需要可写目录; 必须在 import rag 之前设置
os.environ["AIOPS_DATA_DIR"] = tempfile.mkdtemp(prefix="aiops_test_playbooks_")

import numpy as np
import pytest

from rag import RAGStore
from playbook_loader import load_playbooks

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
    return RAGStore(persist_dir=str(tmp_path))


def test_load_playbooks_returns_chunk_count(store):
    n = load_playbooks(store, playbooks_dir=FIXTURES)
    # 2 篇 playbook, 每篇至少 intro + 4 个 section = 5 chunk
    assert n >= 10
    assert store._playbooks_collection().count() == n


def test_load_playbooks_idempotent(store):
    n1 = load_playbooks(store, playbooks_dir=FIXTURES)
    n2 = load_playbooks(store, playbooks_dir=FIXTURES)
    assert n1 == n2
    # upsert 幂等: 重复加载不产生重复 chunk
    assert store._playbooks_collection().count() == n1


def test_frontmatter_fields_in_metadata(store):
    load_playbooks(store, playbooks_dir=FIXTURES)
    coll = store._playbooks_collection()
    got = coll.get(where={"path": "diagnostics/oom-killed.md"})
    assert got["ids"], "oom-killed playbook 未入库"
    meta = got["metadatas"][0]
    assert meta["category"] == "diagnostics"
    assert meta["title"] == "OOMKilled 容器内存溢出"
    assert "oom" in meta["tags"]
    assert "ContainerOOMKilled" in meta["alert_keys"]
    assert "k8s" in meta["applies_to"]


def test_chunks_split_by_sections(store):
    load_playbooks(store, playbooks_dir=FIXTURES)
    coll = store._playbooks_collection()
    got = coll.get(where={"path": "diagnostics/oom-killed.md"})
    # intro + What this means + Immediate checks + Likely causes + Escalation criteria
    assert len(got["ids"]) >= 5
    docs = got["documents"]
    # 每个 section 标题独立成块
    assert any(d.startswith("## What this means") for d in docs)
    assert any(d.startswith("## Immediate checks") for d in docs)
    # 单块不超过 600 字符
    assert all(len(d) <= 600 for d in docs)


def test_load_playbooks_default_dir_exists():
    from playbook_loader import _default_playbooks_dir
    assert os.path.isdir(_default_playbooks_dir())
