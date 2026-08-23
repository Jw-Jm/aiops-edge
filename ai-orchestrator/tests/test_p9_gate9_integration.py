"""Gate 9 集成测试 — V9.3 Phase9（评审加固后）。

验证完整链：
  Evidence → Hypothesis → Support → Contradiction → Missing → Follow-up → Re-score → Root Cause → Confidence → Unknowns

评审修复：ScoringEngine 消费 RcaEvaluationInput（内部计算分量），
Gate 9 断言通过结构化组件验证，不再直接传分量。

Gate 9 追加断言（§七十五）：
  1. same Evidence snapshot produces reproducible score components
  2. contradictory evidence lowers/blocks confirmation
  3. missing critical evidence blocks automatic remediation
  4. prompt-only RCA path absent
"""
from datetime import datetime, timezone
import uuid

import pytest

import contracts as C

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
_H = "5c5bdbb2-8f0d-5b0e-a2e1-9a1b2c3d4e5f"


def _eid(label):
    """确定性 Evidence UUID（替代平行字符串 evidence_id）。"""
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _evidence(evidence_id, etype, source, reliability, fact, run_id=RUN):
    from evidence_hub import Evidence

    return Evidence(
        C.Evidence(
            evidence_id=_eid(evidence_id),
            run_id=run_id,
            tenant_id=TENANT,
            cluster_id=CLUSTER,
            evidence_type=etype,
            claim_type="fact",
            source=source,
            source_reliability=reliability,
            fact=fact,
            raw_digest_sha256=f"digest-{evidence_id}",
            provenance_fingerprint=f"fp-{evidence_id}",
            created_at=datetime.now(timezone.utc),
        )
    )


def _eid(label):
    """确定性 Evidence UUID（替代平行字符串 evidence_id）。"""
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _hypothesis(hid=_H):
    from hypothesis import Hypothesis

    return Hypothesis(
        C.Hypothesis(
            hypothesis_id=hid, run_id=RUN,
            title="变更发布导致错误率上升", description="新版本回归",
            confidence=0.0, status=C.HypothesisStatus.CANDIDATE,
            tenant_id=TENANT, cluster_id=CLUSTER, resource_id="cluster-1/svc",
            affected_resource="svc",
        ),
        required_support=["metric_anomaly", "change"],
        potential_contradiction=["change_after_fault"],
    )


def _eval(support_matcher=None, contradiction_checker=None, missing_engine=None,
          llm_prior=0.8, evidence_ids=None, timeline=None, hypothesis=None):
    from rca_snapshot import RcaInputSnapshot
    from support_matcher import SupportMatcher
    from contradiction_checker import ContradictionChecker
    from missing_evidence import MissingEvidenceEngine
    from scoring_engine import RcaEvaluationInput

    h = hypothesis or _hypothesis()
    snap = RcaInputSnapshot(
        run_id=RUN, intent_id="intent-1",
        evidence_ids=evidence_ids or [_eid("ev-metric"), _eid("ev-change")],
        snapshot_version="v1", tenant_id=TENANT, cluster_id=CLUSTER,
    )
    return RcaEvaluationInput(
        snapshot=snap,
        hypothesis=h,
        support_matcher=support_matcher or SupportMatcher(),
        contradiction_checker=contradiction_checker or ContradictionChecker(),
        missing_engine=missing_engine or MissingEvidenceEngine(),
        timeline=timeline,
        llm_reasoning_prior=llm_prior,
    )


def _support_for_confirmed():
    from support_matcher import SupportMatcher

    sm = SupportMatcher()
    sm.add_relation(_H, _eid("ev-metric"), 0.95, "direct_support")
    sm.add_relation(_H, _eid("ev-change"), 0.9, "direct_support")
    return sm


def test_gate9_full_chain_to_confirmed_root_cause():
    from scoring_engine import ScoringEngine
    from root_cause_ranker import RootCauseRanker

    sm = _support_for_confirmed()
    result = ScoringEngine().score(_eval(support_matcher=sm, llm_prior=0.8))
    ranked = RootCauseRanker().rank([_ranked_hyp(_H, result.final_score, direct_rel=0.95)])
    assert ranked.root_cause is not None
    assert ranked.confidence_state == "confirmed"


def _ranked_hyp(hid, final_score, direct_rel):
    from types import SimpleNamespace

    return SimpleNamespace(
        hypothesis_id=hid,
        claim="claim",
        final_score=final_score,
        support=0.0,
        direct_evidence_reliability=direct_rel,
        has_unresolved_critical=False,
        has_critical_missing=False,
    )


# ---- Gate 9 断言 1: 可复现评分分量 ----
def test_gate9_assert_reproducible_score():
    from scoring_engine import ScoringEngine

    sm = _support_for_confirmed()
    a = ScoringEngine().score(_eval(support_matcher=sm, llm_prior=0.8))
    b = ScoringEngine().score(_eval(support_matcher=sm, llm_prior=0.8))
    assert a.base_score == b.base_score
    assert a.final_score == b.final_score
    assert a.components == b.components


# ---- Gate 9 断言 2: 矛盾降低/阻断确认 ----
def test_gate9_assert_contradiction_blocks_confirmation():
    from contradiction_checker import ContradictionChecker
    from root_cause_ranker import RootCauseRanker
    from scoring_engine import ScoringEngine

    # 高分数但 unresolved critical contradiction → 非 confirmed
    h = _ranked_hyp(_H, 0.90, 0.95)
    h.has_unresolved_critical = True
    assert RootCauseRanker().confidence_state(h) != "confirmed"

    # contradiction 会降低 score（penalty 内部计算）
    sm = _support_for_confirmed()
    cc = ContradictionChecker()
    cc.add_contradiction(_H, "ev-x", "time_conflict", "critical")
    with_ct = ScoringEngine().score(_eval(support_matcher=sm, contradiction_checker=cc, llm_prior=0.8)).final_score
    without_ct = ScoringEngine().score(_eval(support_matcher=sm, llm_prior=0.8)).final_score
    assert with_ct < without_ct


# ---- Gate 9 断言 3: missing critical evidence blocks auto remediation ----
def test_gate9_assert_missing_critical_blocks_remediation():
    from missing_evidence import MissingEvidenceEngine
    from unknown_handler import UnknownSafeHandler

    eng = MissingEvidenceEngine()
    eng.add_missing(hypothesis_id=_H, required_type="trace_anomaly",
                    critical=True, reason="insufficient_data")
    assert eng.blocks_confirmation(_H) is True
    result = UnknownSafeHandler().handle(
        run_id="run-1", root_cause=None, missing_evidence=["trace_anomaly"])
    assert result.automatic_remediation is False
    assert result.ops_actions == []


# ---- Gate 9 断言 4: prompt-only RCA path absent ----
def test_gate9_assert_no_prompt_only_rca():
    from rca_snapshot import RcaInputSnapshot, RcaSnapshotError

    # 没有任何 Evidence 的 snapshot → 无法支撑 scoring（禁 LLM/prompt 自述）
    snap = RcaInputSnapshot(run_id="run-1", intent_id="intent-1",
                            evidence_ids=(), snapshot_version="v1")
    with pytest.raises(RcaSnapshotError):
        snap.assert_evidence_registered("llm-invented-evidence")
    assert len(snap.evidence_ids) == 0
    # 空 snapshot 的 support 必须为 0，无法达 confirmed
    from scoring_engine import ScoringEngine
    from rca_snapshot import RcaInputSnapshot
    from support_matcher import SupportMatcher
    from contradiction_checker import ContradictionChecker
    from missing_evidence import MissingEvidenceEngine
    from scoring_engine import RcaEvaluationInput

    empty_snap = RcaInputSnapshot(run_id="run-1", intent_id="intent-1", evidence_ids=(),
                                  snapshot_version="v1", tenant_id="tenant-1", cluster_id="cluster-1")
    inp = RcaEvaluationInput(
        snapshot=empty_snap, hypothesis=_hypothesis(),
        support_matcher=SupportMatcher(), contradiction_checker=ContradictionChecker(),
        missing_engine=MissingEvidenceEngine(), llm_reasoning_prior=0.8,
    )
    result = ScoringEngine().score(inp)
    assert result.components["evidence_support"] == 0.0
    assert result.final_score < 0.80


# ---- 完整链 follow-up + re-score ----
def test_gate9_followup_rescore_chain():
    from rca_snapshot import RcaInputSnapshot
    from missing_evidence import MissingEvidenceEngine
    from followup_planner import FollowUpPlanner

    snap = RcaInputSnapshot(run_id="run-1", intent_id="intent-1",
                            evidence_ids=["ev-metric"], snapshot_version="v1",
                            tenant_id="tenant-1", cluster_id="cluster-1")
    eng = MissingEvidenceEngine()
    m = eng.add_missing(hypothesis_id=_H, required_type="trace_anomaly",
                        critical=True, reason="insufficient_data", followup_slot="tool:query_traces")

    fp = FollowUpPlanner(max_steps=5)
    req = fp.propose_followup(_H, m.missing_id, "query_traces", "trace.read", budget_cost=1)
    fp.accept(req.followup_id)
    assert fp.consumed_steps == 1

    # 补查后 evidence 回填 → 新 snapshot（Re-score 用新版本）
    new_snap = snap.add_evidence("ev-trace", evidence_cluster="cluster-1")
    assert "ev-trace" in new_snap.evidence_ids
    assert new_snap.snapshot_version != snap.snapshot_version
    assert fp.investigation_graph_id == "primary"
