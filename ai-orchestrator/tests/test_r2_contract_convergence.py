"""R2 Task2-4 收敛契约测试（2026-08-21）。

验证：
- T2: EvidenceLifecycleState / EvidenceState（生命周期外部化）
- T3: PlannerState / PlannerBudget（预算固化，透支拒绝）
- T4: RcaResult（强隔离 + Unknown-safe）+ HypothesisScore/Contradiction/MissingEvidence
"""
import uuid
from datetime import datetime, timezone

import pytest

import contracts


def _now():
    return datetime.now(timezone.utc)


def _u():
    return uuid.uuid4()


# ── T2: EvidenceLifecycleState ───────────────────────────────────────────

def test_evidence_lifecycle_state_enum():
    assert contracts.EvidenceLifecycleState.CREATED.value == "created"
    assert contracts.EvidenceLifecycleState.VALIDATED.value == "validated"
    assert contracts.EvidenceLifecycleState.EXPIRED.value == "expired"
    assert contracts.EvidenceLifecycleState.ARCHIVED.value == "archived"


def test_evidence_state_holds_lifecycle_not_in_evidence():
    """生命周期按 evidence_id 外部管理，不污染不可变 Evidence 本体。"""
    eid = _u()
    st = contracts.EvidenceState(
        evidence_id=eid,
        status=contracts.EvidenceLifecycleState.VALIDATED,
        updated_at=_now(),
        version=2,
    )
    assert st.evidence_id == eid
    assert st.status.value == "validated"
    assert st.version == 2
    # Evidence 本体不含 status 字段
    contracts.Evidence(
        evidence_id=eid,
        run_id=_u(), tenant_id=_u(), cluster_id=_u(),
        evidence_type="metric_anomaly", claim_type="fact",
        source="VM", source_reliability=0.95, fact="spike",
        resource_id="svc:orders",  # fact evidence 需引用场数据
        raw_digest_sha256="aabbccdd",
        provenance_fingerprint="fp", created_at=_now(),
    )
    assert "status" not in contracts.Evidence.model_fields


# ── T3: PlannerState 预算固化 ────────────────────────────────────────────

def test_planner_budget_defaults():
    b = contracts.PlannerBudget()
    assert b.max_steps == 20
    assert b.consumed_steps == 0
    assert b.consumed_followup_rounds == 0


def test_planner_state_rejects_budget_overrun():
    """预算透支必须拒绝（PlannerState 预算固化）。"""
    uid = _u()
    plan = contracts.InvestigationPlan(plan_id=uid, run_id=uid, goal="g", target={}, steps=[])
    with pytest.raises(ValueError):
        contracts.PlannerState(
            run_id=uid, plan=plan,
            budget=contracts.PlannerBudget(max_steps=2, consumed_steps=5),
        )


def test_planner_state_accepts_within_budget():
    uid = _u()
    plan = contracts.InvestigationPlan(plan_id=uid, run_id=uid, goal="g", target={}, steps=[])
    ps = contracts.PlannerState(
        run_id=uid, plan=plan,
        budget=contracts.PlannerBudget(max_steps=20, consumed_steps=3),
    )
    assert ps.budget.consumed_steps == 3
    assert ps.status == contracts.PlanStepStatus.PENDING


# ── T4: RcaResult 强隔离 + Unknown-safe ─────────────────────────────────

def test_rca_result_requires_isolation():
    """RcaResult 强隔离：必须含 tenant/cluster/run/resource。"""
    uid = _u()
    r = contracts.RcaResult(
        rca_id=uid, run_id=uid, tenant_id=uid, cluster_id=uid,
        resource_id="svc:orders", root_cause="latency", confidence=0.85,
    )
    assert r.root_cause == "latency"
    assert r.automatic_remediation is False


def test_rca_result_unknown_rejects_auto_remediation():
    """Unknown root_cause 不得 automatic_remediation（Unknown-safe）。"""
    uid = _u()
    with pytest.raises(ValueError):
        contracts.RcaResult(
            rca_id=uid, run_id=uid, tenant_id=uid, cluster_id=uid,
            resource_id="x", root_cause=None, confidence=0.0,
            status="unknown", automatic_remediation=True,
        )


def test_rca_result_score_breakdown():
    uid = _u()
    r = contracts.RcaResult(
        rca_id=uid, run_id=uid, tenant_id=uid, cluster_id=uid, resource_id="x",
        root_cause="latency", confidence=0.82,
        hypothesis_scores=[
            contracts.HypothesisScore(
                llm_prior=0.5, evidence_support=0.8, source_reliability=0.9,
                temporal=1.0, contradiction_penalty=0.0, missing_penalty=0.0,
                final_score=0.82,
            )
        ],
        contradictions=[
            contracts.Contradiction(kind="change_after_fault", severity="normal", detail="d")
        ],
        missing_evidence=[
            contracts.MissingEvidence(kind="critical", reason="k8s_state_missing", description="d")
        ],
    )
    assert r.hypothesis_scores[0].final_score == 0.82
    assert r.contradictions[0].severity == "normal"
    assert r.missing_evidence[0].kind == "critical"
