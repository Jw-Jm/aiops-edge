"""P9.8 Root Cause Ranker — V9.3 Phase9（TDD RED 测试）。

排序时输出：score、support、contradictions、missing、confidence state。
confirmed 必须满足原文条件（§四十）：
  final_score >= 0.80 AND >=1 direct evidence reliability >=0.85 AND no unresolved critical contradiction
supported: 0.60 <= final_score < 0.80
"""
import pytest


def _hyp(**kw):
    from dataclasses import dataclass

    @dataclass
    class H:
        hypothesis_id: str = "h-1"
        claim: str = "claim"
        final_score: float = 0.0
        support: float = 0.0
        direct_evidence_reliability: float = 0.0
        has_unresolved_critical: bool = False
        has_critical_missing: bool = False

    return H(**kw)


def test_ranker_confirms_when_thresholds_met():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h = _hyp(final_score=0.85, direct_evidence_reliability=0.9)
    state = ranker.confidence_state(h)
    assert state == "confirmed"


def test_ranker_requires_direct_evidence_ge_085():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    # 分数达标但 direct evidence < 0.85 → 非 confirmed
    h = _hyp(final_score=0.85, direct_evidence_reliability=0.80)
    assert ranker.confidence_state(h) != "confirmed"


def test_ranker_requires_score_ge_080():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h = _hyp(final_score=0.75, direct_evidence_reliability=0.9)
    assert ranker.confidence_state(h) != "confirmed"


def test_ranker_unresolved_critical_blocks_confirmed():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h = _hyp(final_score=0.90, direct_evidence_reliability=0.9, has_unresolved_critical=True)
    assert ranker.confidence_state(h) != "confirmed"


def test_ranker_supported_range():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h = _hyp(final_score=0.70, direct_evidence_reliability=0.9)
    assert ranker.confidence_state(h) == "supported"


def test_ranker_ranks_by_score_and_selects_root_cause():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h1 = _hyp(hypothesis_id="h-1", final_score=0.9, direct_evidence_reliability=0.9)
    h2 = _hyp(hypothesis_id="h-2", final_score=0.6, direct_evidence_reliability=0.9)
    ranking = ranker.rank([h2, h1])  # 乱序输入
    assert [r.hypothesis_id for r in ranking.ranked] == ["h-1", "h-2"]  # 按分降序
    assert ranking.root_cause.hypothesis_id == "h-1"
    assert ranking.confidence_state == "confirmed"


def test_ranker_returns_unknown_when_no_confirmed():
    from root_cause_ranker import RootCauseRanker

    ranker = RootCauseRanker()
    h = _hyp(hypothesis_id="h-1", final_score=0.5, direct_evidence_reliability=0.9)
    ranking = ranker.rank([h])
    assert ranking.root_cause is None
    assert ranking.confidence_state == "unknown"
