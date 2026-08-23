"""P9.8 Root Cause Ranker — 评审加固测试（状态矩阵）。

评审修复（P1）：
- 低于 0.60 → unknown（不是 rejected）。
- rejected 只适用于反证明显压倒支持（unresolved critical contradiction）。
- 分数高于 0.80 但缺可靠直接证据 → unknown（不是 rejected）。
- 完整互斥状态判定矩阵。
"""
from types import SimpleNamespace

import pytest


def _hyp(final_score, direct_rel, has_critical=False, has_missing=False):
    return SimpleNamespace(
        hypothesis_id="h-1", claim="c", final_score=final_score, support=0.0,
        direct_evidence_reliability=direct_rel,
        has_unresolved_critical=has_critical, has_critical_missing=has_missing,
    )


def test_confirmed_when_all_thresholds_met():
    from root_cause_ranker import RootCauseRanker

    assert RootCauseRanker().confidence_state(
        _hyp(0.85, 0.9)) == "confirmed"


def test_low_score_below_supported_is_unknown_not_rejected():
    from root_cause_ranker import RootCauseRanker

    # 0.5 < 0.60 → unknown（不是 rejected）
    assert RootCauseRanker().confidence_state(_hyp(0.5, 0.9)) == "unknown"


def test_high_score_missing_direct_evidence_is_unknown():
    from root_cause_ranker import RootCauseRanker

    # 分数>=0.80 但 direct<0.85 → unknown（不是 rejected）
    assert RootCauseRanker().confidence_state(_hyp(0.85, 0.80)) == "unknown"


def test_rejected_only_when_critical_contradiction_overwhelms():
    from root_cause_ranker import RootCauseRanker

    # unresolved critical contradiction → rejected（反证压倒支持）
    assert RootCauseRanker().confidence_state(_hyp(0.85, 0.9, has_critical=True)) == "rejected"


def test_critical_missing_is_unknown():
    from root_cause_ranker import RootCauseRanker

    # critical missing 限制最终状态 → unknown
    assert RootCauseRanker().confidence_state(_hyp(0.85, 0.9, has_missing=True)) == "unknown"


def test_supported_range():
    from root_cause_ranker import RootCauseRanker

    assert RootCauseRanker().confidence_state(_hyp(0.70, 0.9)) == "supported"


def test_states_are_mutually_exclusive():
    from root_cause_ranker import RootCauseRanker, CONFIDENCE_STATES

    states = {RootCauseRanker().confidence_state(
        _hyp(fs, dr, hc, hm))
        for fs in (0.5, 0.7, 0.85) for dr in (0.80, 0.9)
        for hc in (False, True) for hm in (False, True)}
    # 每个输入只产出一个状态，且都合法
    assert states <= CONFIDENCE_STATES
