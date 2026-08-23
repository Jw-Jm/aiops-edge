"""R2 收敛 — 权威子合同 V2 演进测试（contracts.py）。

依据设计 v0.3 §2/§5：
- HypothesisScore/Contradiction/MissingEvidence 新增关联字段（hypothesis_id/evidence_id/
  required_type/followup_slot），可无损反查，杜绝有损映射。
- RcaResult.conclusion_state + state_matrix validator（confirmed/supported/rejected 严格校验）。
"""
import pytest
from uuid import UUID


def _hid():
    return UUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")


def _rc():
    from contracts import RcaResult
    return RcaResult


def test_hypothesis_score_carries_hypothesis_id():
    from contracts import HypothesisScore
    hs = HypothesisScore(
        llm_prior=0.5, evidence_support=0.6, source_reliability=0.7, temporal=0.8,
        contradiction_penalty=-0.1, missing_penalty=-0.05, final_score=0.82,
        hypothesis_id=_hid(),
    )
    assert hs.hypothesis_id == _hid()


def test_contradiction_carries_hypothesis_and_evidence():
    from contracts import Contradiction
    c = Contradiction(
        kind="resource_cluster_conflict", severity="critical", detail="cross-cluster",
        hypothesis_id=_hid(), evidence_id=UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
    )
    assert c.hypothesis_id == _hid()
    assert c.evidence_id == UUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")


def test_missing_evidence_carries_required_type_and_slot():
    from contracts import MissingEvidence
    m = MissingEvidence(
        kind="critical", reason="missing change evidence",
        hypothesis_id=_hid(), required_type="change", followup_slot="followup-1",
    )
    assert m.hypothesis_id == _hid()
    assert m.required_type == "change"
    assert m.followup_slot == "followup-1"


def test_state_matrix_confirmed_requires_confidence_ge_080():
    from contracts import RcaResult, RootCauseRef
    with pytest.raises(ValueError):
        RcaResult(
            rca_id=UUID("11111111-1111-4111-8111-111111111111"),
            run_id=UUID("22222222-2222-4222-8222-222222222222"),
            tenant_id=UUID("33333333-3333-4333-8333-333333333333"),
            cluster_id=UUID("44444444-4444-4444-8444-444444444444"),
            resource_id="cluster-1/svc/checkout",
            root_cause="checkout error", confidence=0.79, conclusion_state="confirmed",
            root_cause_refs=[RootCauseRef(hypothesis_id=_hid(), final_score=0.79)],
        )


def test_state_matrix_supported_requires_refs():
    from contracts import RcaResult
    with pytest.raises(ValueError):
        RcaResult(
            rca_id=UUID("11111111-1111-4111-8111-111111111111"),
            run_id=UUID("22222222-2222-4222-8222-222222222222"),
            tenant_id=UUID("33333333-3333-4333-8333-333333333333"),
            cluster_id=UUID("44444444-4444-4444-8444-444444444444"),
            resource_id="cluster-1/svc/checkout",
            root_cause="checkout error", confidence=0.70, conclusion_state="supported",
            root_cause_refs=[],
        )


def test_state_matrix_rejected_requires_none_root_cause():
    from contracts import RcaResult
    with pytest.raises(ValueError):
        RcaResult(
            rca_id=UUID("11111111-1111-4111-8111-111111111111"),
            run_id=UUID("22222222-2222-4222-8222-222222222222"),
            tenant_id=UUID("33333333-3333-4333-8333-333333333333"),
            cluster_id=UUID("44444444-4444-4444-8444-444444444444"),
            resource_id="cluster-1/svc/checkout",
            root_cause="checkout error", confidence=0.30, conclusion_state="rejected",
        )


def test_state_matrix_confirmed_valid():
    from contracts import RcaResult, RootCauseRef
    r = RcaResult(
        rca_id=UUID("11111111-1111-4111-8111-111111111111"),
        run_id=UUID("22222222-2222-4222-8222-222222222222"),
        tenant_id=UUID("33333333-3333-4333-8333-333333333333"),
        cluster_id=UUID("44444444-4444-4444-8444-444444444444"),
        resource_id="cluster-1/svc/checkout",
        root_cause="checkout error", confidence=0.85, conclusion_state="confirmed",
        root_cause_refs=[RootCauseRef(hypothesis_id=_hid(), final_score=0.85)],
    )
    assert r.conclusion_state == "confirmed"
