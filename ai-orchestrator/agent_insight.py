"""ARI.4 AgentInsight 协议冻结（N3）— V9.3 Agent Runtime Integration。

统一 Agent 输出协议（评审 N3 建议冻结）：
  AgentInsight { agent_type, evidence_refs[], insights[], confidence, missing_slots[] }

约束：
- 所有 7 类 Agent 统一输出 AgentInsight（禁止各 Agent 自定义输出协议）。
- confidence 是 evidence_confidence（证据置信度 0-1），不是"根因正确概率"（Agent 不归因）。
- AgentInsight 无 root_cause（Agent 不产出最终根因）。
- missing_slots 与 Planner follow-up 关联（tool_id/capability/reason）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List

AGENT_TYPES = {
    "observability", "log", "trace", "kubernetes", "change", "knowledge", "infrastructure",
}


class AgentInsightError(ValueError):
    def __init__(self, message: str):
        self.error_code = "AGENT_INSIGHT_ERROR"
        super().__init__(message)


@dataclass
class AgentInsight:
    agent_type: str
    evidence_refs: List[str] = field(default_factory=list)
    insights: List[Dict[str, Any]] = field(default_factory=list)
    confidence: float = 0.0
    missing_slots: List[Dict[str, Any]] = field(default_factory=list)

    def __post_init__(self) -> None:
        if self.agent_type not in AGENT_TYPES:
            raise AgentInsightError(f"非法 agent_type: {self.agent_type}")
        if not (0.0 <= self.confidence <= 1.0):
            raise AgentInsightError(f"confidence 超出 [0,1]: {self.confidence}")
