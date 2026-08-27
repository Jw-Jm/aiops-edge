from __future__ import annotations
from typing import Any


def explain(payload: dict[str, Any], llm: Any = None) -> str:
    """LLM is an explanation consumer; it cannot mutate scores or graph facts."""
    if llm is not None:
        value = llm(payload)
        if isinstance(value, str):
            return value
    status = payload.get("root_cause_status", "insufficient_evidence")
    root = payload.get("root_cause") or "未确认根因"
    return f"根因状态：{status}；候选：{root}。证据类别数：{len(payload.get('evidence', []))}。"
