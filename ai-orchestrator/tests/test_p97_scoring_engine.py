"""P9.7 Fixed Scoring Engine — V9.3 Phase9（评审加固后测试）。

评审修复：ScoringEngine 改为消费结构化 RcaEvaluationInput，内部从 Evidence 计算
support/reliability/temporal/penalty，禁止外部直接传最终分量（防调用方伪造分数）。

base_score = llm_prior*0.35 + evidence_support*0.30 + source_reliability*0.20 + temporal_relation*0.15
penalty: critical=-0.25 cap-0.50, normal=-0.10 cap-0.30, missing critical=-0.20 cap-0.40
final_score = clamp(base_score - penalties, 0, 1)
"""
import pytest


# 固定 Hypothesis UUID（迁移：平行字符串 "h-1" → 权威 UUID 确定性派生）
_H = "5c5bdbb2-8f0d-5b0e-a2e1-9a1b2c3d4e5f"


def _eval_input(**kw):
    """构造 RcaEvaluationInput，附带可注入的 support/contradiction/missing/timeline。"""
    import contracts as C
    from rca_snapshot import RcaInputSnapshot
    from hypothesis import Hypothesis
    from support_matcher import SupportMatcher
    from contradiction_checker import ContradictionChecker
    from missing_evidence import MissingEvidenceEngine
    from scoring_engine import RcaEvaluationInput

    RUN = kw.get("run_id", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
    TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    hypothesis = kw.get("hypothesis") or Hypothesis(
        C.Hypothesis(
            hypothesis_id=kw.get("hypothesis_id") or _H,
            run_id=RUN, title="c", description="m",
            confidence=0.0, status=C.HypothesisStatus.CANDIDATE,
            tenant_id=TENANT, cluster_id=CLUSTER, resource_id="cluster-1/svc",
            affected_resource="r",
        )
    )
    snapshot = kw.get("snapshot") or RcaInputSnapshot(
        run_id=RUN, intent_id="intent-1",
        evidence_ids=kw.get("evidence_ids", ["ev-1", "ev-2"]),
        snapshot_version="v1", tenant_id=TENANT, cluster_id=CLUSTER,
    )
    sm = kw.get("support_matcher") or SupportMatcher()
    cc = kw.get("contradiction_checker") or ContradictionChecker()
    me = kw.get("missing_engine") or MissingEvidenceEngine()
    tl = kw.get("timeline")
    return RcaEvaluationInput(
        snapshot=snapshot,
        hypothesis=hypothesis,
        support_matcher=sm,
        contradiction_checker=cc,
        missing_engine=me,
        timeline=tl,
        llm_reasoning_prior=kw.get("llm_reasoning_prior", 0.0),
    )


def _score(**kw):
    from scoring_engine import ScoringEngine

    return ScoringEngine().score(_eval_input(**kw))


def test_scoring_requires_evaluation_input_not_raw_components():
    from scoring_engine import ScoringEngine

    # score() 不接受裸 support/reliability/temporal 数值（防调用方伪造）
    with pytest.raises(TypeError):
        ScoringEngine().score(
            hypothesis_id="h-1", llm_reasoning_prior=1.0,
            evidence_support=1.0, source_reliability=1.0, temporal_relation=1.0,
        )


def test_support_computed_internally_from_matcher():
    from support_matcher import SupportMatcher

    sm = SupportMatcher()
    sm.add_relation(_H, "ev-1", 0.95, "direct_support")
    sm.add_relation(_H, "ev-2", 0.85, "indirect_support")
    result = _score(support_matcher=sm, llm_reasoning_prior=1.0, evidence_ids=["ev-1", "ev-2"])
    # evidence_support = (0.95*1.0 + 0.85*0.6)/(0.95+0.85)
    expected_support = (0.95 * 1.0 + 0.85 * 0.6) / (0.95 + 0.85)
    assert result.components["evidence_support"] == pytest.approx(expected_support, abs=1e-9)
    # source_reliability = unique supporting reliability 均值
    assert result.components["source_reliability"] == pytest.approx((0.95 + 0.85) / 2, abs=1e-9)


def test_contradiction_penalty_from_checker():
    from contradiction_checker import ContradictionChecker

    cc = ContradictionChecker()
    cc.add_contradiction(_H, "ev-x", "time_conflict", "critical")
    result = _score(contradiction_checker=cc, llm_reasoning_prior=1.0,
                    evidence_ids=["ev-1"])
    # 验证 penalty 分量（critical contradiction）
    assert any(p.type == "critical_contradiction" for p in result.penalties)


def test_missing_critical_penalty_from_engine():
    from missing_evidence import MissingEvidenceEngine

    me = MissingEvidenceEngine()
    me.add_missing(_H, "trace_anomaly", critical=True, reason="insufficient_data")
    result = _score(missing_engine=me, llm_reasoning_prior=1.0, evidence_ids=["ev-1"])
    assert any(p.type == "missing_critical" for p in result.penalties)


def test_penalty_critical_cap_at_minus_050():
    from contradiction_checker import ContradictionChecker

    cc = ContradictionChecker()
    for i in range(3):
        cc.add_contradiction(_H, f"ev-c{i}", "time_conflict", "critical")
    # 3 critical → cap -0.50
    result = _score(contradiction_checker=cc, llm_reasoning_prior=1.0, evidence_ids=["ev-1"])
    crit = next(p for p in result.penalties if p.type == "critical_contradiction")
    assert crit.value == pytest.approx(0.50, abs=1e-9)


def test_score_clamped_to_zero():
    from contradiction_checker import ContradictionChecker

    cc = ContradictionChecker()
    for i in range(5):
        cc.add_contradiction(_H, f"ev-c{i}", "time_conflict", "critical")
    result = _score(contradiction_checker=cc, llm_reasoning_prior=0.0, evidence_ids=["ev-1"])
    assert result.final_score == 0.0


def test_reproducible_same_inputs():
    a = _score(llm_reasoning_prior=0.8, evidence_ids=["ev-1", "ev-2"])
    b = _score(llm_reasoning_prior=0.8, evidence_ids=["ev-1", "ev-2"])
    assert a.final_score == b.final_score
    assert a.base_score == b.base_score


def test_breakdown_reports_components():
    result = _score(llm_reasoning_prior=0.8, evidence_ids=["ev-1", "ev-2"])
    assert set(result.components) == {
        "llm_reasoning_prior", "evidence_support", "source_reliability", "temporal_relation",
    }
    assert result.penalties is not None


def test_unregistered_evidence_rejected():
    from rca_snapshot import RcaInputSnapshot
    from support_matcher import SupportMatcher

    # snapshot 只有 ev-1，但 matcher 引用 ev-99 → 未登记拒绝
    snap = RcaInputSnapshot(
        run_id="run-1", intent_id="intent-1", evidence_ids=["ev-1"],
        snapshot_version="v1", tenant_id="tenant-1", cluster_id="cluster-1",
    )
    sm = SupportMatcher()
    sm.add_relation(_H, "ev-99", 0.95, "direct_support")
    from scoring_engine import ScoringEngine
    from rca_snapshot import RcaSnapshotError

    with pytest.raises(RcaSnapshotError):
        ScoringEngine().score(_eval_input(snapshot=snap, support_matcher=sm, evidence_ids=["ev-1"]))
