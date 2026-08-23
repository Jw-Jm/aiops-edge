"""P9.3 Support Matcher — V9.3 Phase9。

把 Evidence→Hypothesis 支持关系结构化，计算 evidence support；
同一 provenance 重复 Evidence 不重复加权（§三十八）。
direct_support=1.0, indirect_support=0.6, top 5 unique supporting evidence。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Tuple

RELATION_WEIGHTS = {
    "direct_support": 1.0,
    "indirect_support": 0.6,
}

VALID_RELATIONS = set(RELATION_WEIGHTS)
MAX_UNIQUE_SUPPORTING = 5


class SupportMatcherError(ValueError):
    def __init__(self, message: str):
        self.error_code = "SUPPORT_MATCHER_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class SupportRelation:
    """一条 Evidence→Hypothesis 支持关系。"""

    support_id: str
    hypothesis_id: str
    evidence_id: str
    relation: str
    source_reliability: float


class SupportMatcher:
    """内存 Support Matcher：维护支持关系，计算 evidence_support（§三十八）。"""

    def __init__(self) -> None:
        self._relations: Dict[str, List[SupportRelation]] = {}
        self._support_seq = 0

    def add_relation(
        self,
        hypothesis_id: str,
        evidence_id: str,
        source_reliability: float,
        relation: str,
    ) -> SupportRelation:
        if relation not in VALID_RELATIONS:
            raise SupportMatcherError(f"非法 relation: {relation}")
        if not (0.0 <= source_reliability <= 1.0):
            raise SupportMatcherError(f"source_reliability 超出 [0,1]: {source_reliability}")
        rels = self._relations.setdefault(hypothesis_id, [])
        # 同一 provenance（evidence_id）重复 → 不重复加权（去重）
        if any(r.evidence_id == evidence_id for r in rels):
            return next(r for r in rels if r.evidence_id == evidence_id)
        self._support_seq += 1
        rel = SupportRelation(
            support_id=f"sup-{self._support_seq}",
            hypothesis_id=hypothesis_id,
            evidence_id=evidence_id,
            relation=relation,
            source_reliability=source_reliability,
        )
        rels.append(rel)
        return rel

    def relations_for(self, hypothesis_id: str) -> List[SupportRelation]:
        return list(self._relations.get(hypothesis_id, []))

    def evidence_support(self, hypothesis_id: str) -> float:
        """§三十八 evidence_support = Σ(reliability×weight) / Σ(reliability)，取 top 5 unique。"""
        rels = self._relations.get(hypothesis_id, [])
        if not rels:
            return 0.0
        # top 5 unique supporting evidence（按 source_reliability 降序取前 5）
        top = sorted(rels, key=lambda r: r.source_reliability, reverse=True)[:MAX_UNIQUE_SUPPORTING]
        numerator = sum(r.source_reliability * RELATION_WEIGHTS[r.relation] for r in top)
        denominator = sum(r.source_reliability for r in top)
        if denominator == 0:
            return 0.0
        return numerator / denominator
