"""P9.8 Root Cause Ranker — V9.3 Phase9。

排序时输出：score、support、contradictions、missing、confidence state。
confirmed 必须满足原文条件（§四十）：
  final_score >= 0.80 AND >=1 direct evidence reliability >=0.85 AND no unresolved critical contradiction
supported: 0.60 <= final_score < 0.80
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, List, Optional

CONFIRMED_MIN_SCORE = 0.80
CONFIRMED_MIN_DIRECT_RELIABILITY = 0.85
SUPPORTED_MIN_SCORE = 0.60

CONFIDENCE_STATES = {"confirmed", "supported", "rejected", "unknown"}


@dataclass(frozen=True)
class RankedHypothesis:
    hypothesis_id: str
    final_score: float
    support: float = 0.0
    contradictions: List[Any] = None
    missing: List[Any] = None
    confidence_state: str = "unknown"


@dataclass(frozen=True)
class RootCauseRanking:
    run_id: str = ""
    ranked: List[RankedHypothesis] = None
    root_cause: Optional[RankedHypothesis] = None
    confidence_state: str = "unknown"
    unknowns: List[str] = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "ranked", list(self.ranked or []))
        object.__setattr__(self, "unknowns", list(self.unknowns or []))


class RootCauseRanker:
    """排序 hypothesis，按 §四十 判定 confidence state，选出 root cause。"""

    def confidence_state(
        self,
        h: Any,
    ) -> str:
        """判定单个 hypothesis 的 confidence state（§四十 + 评审互斥矩阵）。

        互斥矩阵：
        - confirmed: score>=0.80 AND direct>=0.85 AND no unresolved critical AND no critical missing
        - supported: 0.60<=score<0.80 AND no unresolved critical AND no critical missing
        - rejected:  unresolved critical contradiction（反证明显压倒支持）
        - unknown:   score<0.60（无法达支持阈值）或 score>=0.80 但 direct<0.85（缺可靠直接证据）
                     或 critical missing（数据不足）
        """
        score = getattr(h, "final_score", 0.0)
        direct_rel = getattr(h, "direct_evidence_reliability", 0.0)
        has_critical = getattr(h, "has_unresolved_critical", False)
        # critical missing 也限制最终状态
        has_missing = getattr(h, "has_critical_missing", False)

        # rejected 只适用于反证明显压倒支持（unresolved critical contradiction）
        if has_critical:
            return "rejected"
        if has_missing:
            return "unknown"
        if score >= CONFIRMED_MIN_SCORE and direct_rel >= CONFIRMED_MIN_DIRECT_RELIABILITY:
            return "confirmed"
        if score >= CONFIRMED_MIN_SCORE:
            # 分数达标但缺可靠直接证据 → unknown（不判 rejected）
            return "unknown"
        if SUPPORTED_MIN_SCORE <= score < CONFIRMED_MIN_SCORE:
            return "supported"
        # score < 0.60 → unknown（不是 rejected）
        return "unknown"

    def rank(self, hypotheses: List[Any], run_id: str = "") -> RootCauseRanking:
        ranked = []
        for h in hypotheses:
            state = self.confidence_state(h)
            ranked.append(
                RankedHypothesis(
                    hypothesis_id=getattr(h, "hypothesis_id", ""),
                    final_score=getattr(h, "final_score", 0.0),
                    support=getattr(h, "support", 0.0),
                    contradictions=getattr(h, "contradictions", None),
                    missing=getattr(h, "missing", None),
                    confidence_state=state,
                )
            )
        ranked.sort(key=lambda r: r.final_score, reverse=True)

        root_cause = None
        overall = "unknown"
        confirmed = [r for r in ranked if r.confidence_state == "confirmed"]
        if confirmed:
            root_cause = confirmed[0]
            overall = "confirmed"
        elif any(r.confidence_state == "supported" for r in ranked):
            overall = "supported"

        return RootCauseRanking(
            run_id=run_id,
            ranked=ranked,
            root_cause=root_cause,
            confidence_state=overall,
            unknowns=[r.hypothesis_id for r in ranked if r.confidence_state == "unknown"],
        )
