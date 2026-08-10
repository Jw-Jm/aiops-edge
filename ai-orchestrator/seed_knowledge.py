"""批量导入运维故障知识库到 RAG (ChromaDB ops_cases)。
用法:
    python3 seed_knowledge.py [json_path] [persist_dir]
默认: data/knowledge_cases.json, /tmp/ops-cases
"""
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rag import RAGStore  # noqa: E402


def load_and_seed(json_path: str, persist_dir: str) -> dict:
    store = RAGStore(persist_dir)
    with open(json_path, "r", encoding="utf-8") as f:
        cases = json.load(f)
    added, dup = 0, 0
    for c in cases:
        cid = hashlib.md5(c.get("symptom", "").encode()).hexdigest()[:12]
        case = {
            "case_id": cid,
            "service": c.get("service", ""),
            "symptom": c.get("symptom", ""),
            "root_cause": c.get("root_cause", ""),
            "plan": c.get("plan", ""),
            "outcome": c.get("outcome", "success"),
            "report": f"[{c.get('service','')}] 故障案例: {c.get('symptom','')}",
        }
        r = store.add_case(case)
        if r == cid:
            added += 1
        else:
            dup += 1
    try:
        total = store.collection.count()
    except Exception:
        total = added
    return {"added": added, "dup": dup, "total": total}


if __name__ == "__main__":
    json_path = sys.argv[1] if len(sys.argv) > 1 else "data/knowledge_cases.json"
    _data_dir = os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
    persist_dir = sys.argv[2] if len(sys.argv) > 2 else os.path.join(_data_dir, "ops-cases")
    print(f"[seed] 开始导入知识库: {json_path} -> {persist_dir}")
    r = load_and_seed(json_path, persist_dir)
    print(f"[seed] 完成: 新增 {r['added']} 条, 去重跳过 {r['dup']} 条, 当前案例总数 {r['total']}")
