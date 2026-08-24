"""Chroma collection bootstrap（V9.2 Phase 4 P4.7）。

在部署 bootstrap 阶段创建/校验 Chroma collection（ops_cases / ops_playbooks），
使 orchestrator runtime 只做 get_collection（缺失 → readiness FAIL），不再 get_or_create。

用法：python -m rag_bootstrap --persist-dir <dir> [--check-only]
"""

from __future__ import annotations

import argparse
import os
import sys

os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("HF_DATASETS_OFFLINE", "1")

from rag import RAGStore, CASE_COLLECTION, PLAYBOOK_COLLECTION  # noqa: E402


def create_or_validate_collections(persist_dir: str) -> list[str]:
    """幂等创建/校验 collection，返回已就绪的 collection 名列表。

    复用 RAGStore.ensure_collections（与 runtime 一致的嵌入器，离线自动降级 ONNX）。
    """
    res = RAGStore.ensure_collections(persist_dir=persist_dir)
    if res is None:
        raise SystemExit("[RAG-BOOTSTRAP] 嵌入器或目录不可用, 无法创建 collection")
    client, ef = res
    ready = []
    for name in (CASE_COLLECTION, PLAYBOOK_COLLECTION):
        col = client.get_collection(name, embedding_function=ef)
        print(f"[RAG-BOOTSTRAP] {name}: ready, count={col.count()}")
        ready.append(name)
    return ready


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--persist-dir", required=True, help="ChromaDB 持久化目录")
    p.add_argument("--check-only", action="store_true",
                   help="只校验 collection 存在，缺失则非零退出（readiness 用）")
    args = p.parse_args()

    if args.check_only:
        # readiness check：两个 collection 都必须存在。
        import chromadb
        from chromadb.config import Settings
        client = chromadb.PersistentClient(
            path=args.persist_dir, settings=Settings(anonymized_telemetry=False))
        missing = []
        for name in (CASE_COLLECTION, PLAYBOOK_COLLECTION):
            try:
                client.get_collection(name)
            except Exception:
                missing.append(name)
        if missing:
            print(f"[RAG-BOOTSTRAP] check-only FAIL: missing collections {missing}")
            return 1
        print("[RAG-BOOTSTRAP] check-only OK")
        return 0

    create_or_validate_collections(args.persist_dir)
    return 0


if __name__ == "__main__":
    sys.exit(main())
