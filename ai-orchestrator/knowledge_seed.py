"""知识库启动自动加载（幂等）。
上线后往 data/knowledge_cases.json 追加新案例，重启或定时调用本模块即可增量导入，
不会重复插入已存在的案例（按 symptom 去重）。也可通过 API/脚本手动触发。
"""
from __future__ import annotations

import hashlib
import json
import logging
import os

logger = logging.getLogger("knowledge_seed")


def load_case(symptom: str) -> str:
    return hashlib.md5((symptom or "").encode("utf-8")).hexdigest()[:12]


def seed_from_json(json_path: str, persist_dir: str | None = None) -> dict:
    """从 JSON 文件批量导入知识库，幂等去重。返回 {added, dup, total, skipped}。"""
    from rag import RAGStore
    store = RAGStore(persist_dir)
    if not os.path.exists(json_path):
        logger.warning("[knowledge] 知识库文件不存在: %s", json_path)
        return {"added": 0, "dup": 0, "total": 0, "skipped": 1}
    with open(json_path, "r", encoding="utf-8") as f:
        cases = json.load(f)
    added, dup, skipped = 0, 0, 0
    for c in cases:
        symptom = c.get("symptom", "")
        if not symptom:
            skipped += 1
            continue
        cid = load_case(symptom)
        case = {
            "case_id": cid,
            "service": c.get("service", ""),
            "symptom": symptom,
            # 列表接口展示用：title=symptom 前 80 字符，content=现象/根因/方案 结构化文本
            # 透传 JSON 中的 type/tags/title（缺省 type=case、tags 空串），保证新增字段能进 ChromaDB
            "type": c.get("type", "case"),
            "tags": c.get("tags", ""),
            "title": c.get("title") or symptom[:80],
            "content": f"现象: {symptom}\n根因: {c.get('root_cause', '')}\n方案: {c.get('plan', '')}",
            "root_cause": c.get("root_cause", ""),
            "plan": c.get("plan", ""),
            "outcome": c.get("outcome", "success"),
            "report": f"[{c.get('service','')}] 故障案例: {symptom}",
        }
        try:
            r = store.add_case(case)
            if r == cid:
                added += 1
            else:
                dup += 1
        except Exception as e:  # noqa: BLE001
            logger.warning("[knowledge] 案例导入失败: %s", e)
            skipped += 1
    try:
        total = store.collection.count()
    except Exception:
        total = added
    logger.info("[knowledge] 导入完成: 新增 %s, 去重 %s, 跳过 %s, 案例总数 %s",
                added, dup, skipped, total)
    return {"added": added, "dup": dup, "total": total, "skipped": skipped}


def seed_default() -> dict:
    """导入项目内置知识库文件（data/knowledge_cases.json，随镜像分发）。"""
    json_path = os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "data", "knowledge_cases.json")
    return seed_from_json(json_path)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    r = seed_default()
    print(f"[knowledge] {r}")
