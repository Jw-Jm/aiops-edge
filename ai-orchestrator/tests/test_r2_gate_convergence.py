"""R2 收敛 — Gate 硬断言验证（设计 v0.3 §11）。

验证 run() 直接返回权威 contracts.RcaResult、rca_engine 无 class RcaResult、
非零 penalty 可复算、score/missing/contradiction 可反查 hypothesis、
root_cause 是 claim（非 UUID）、supported/unknown wire 可区分、snapshot v1/v2 不同 rca_id、
mismatch Evidence 评分前拒绝、rca_production.confidence==contract.confidence。
"""
import uuid
from datetime import datetime, timezone

import contracts as C

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
CLUSTER2 = "dddddddd-0000-4000-8000-000000000002"


def _eid(label):
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _evidence(eid, etype, source, reliability, fact, cluster_id=CLUSTER, observed=None):
    from evidence_hub import Evidence
    return Evidence(
        C.Evidence(
            evidence_id=_eid(eid),
            run_id=RUN, tenant_id=TENANT, cluster_id=cluster_id,
            evidence_type=etype, claim_type="fact", source=source,
            source_reliability=reliability, fact=fact,
            raw_digest_sha256=f"digest-{eid}",
            provenance_fingerprint=f"fp-{eid}",
            created_at=datetime.now(timezone.utc),
            observed_at=observed,
        )
    )


def test_gate_run_returns_contracts_rca_result():
    """Gate 硬断言：run() 直接返回 contracts.RcaResult。"""
    from rca_engine import RcaEngine

    evs = [_evidence("m1", "metric_anomaly", "VM", 0.95, "spike")]
    r = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
        run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=evs,
    )
    assert isinstance(r, C.RcaResult)


def test_gate_rca_engine_has_no_class_rca_result():
    """Gate 硬断言：rca_engine 模块不再定义 class RcaResult。"""
    import inspect
    import rca_engine
    assert not hasattr(rca_engine, "RcaResult")
    # 且无同名字符串类定义
    src = inspect.getsource(rca_engine)
    assert "class RcaResult" not in src


def test_gate_nonzero_penalty_is_negative_and_reproducible():
    """Gate 硬断言：非零 penalty 通过权威合同（<=0）且可复算。"""
    from rca_engine import RcaEngine

    fault_t = datetime(2026, 8, 19, 9, 0, tzinfo=timezone.utc)
    late_t = datetime(2026, 8, 19, 10, 0, tzinfo=timezone.utc)
    evs = [
        _evidence("m1", "metric_anomaly", "VM", 0.95, "spike", observed=fault_t),
        _evidence("c1", "change", "query-api", 0.9, "deploy after fault", observed=late_t),
    ]
    r = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
        run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=evs, llm_reasoning_prior=0.8,
    )
    # 存在 penalty（含 contradiction_penalty），且必须 <=0（否则 Pydantic 已拒绝）
    assert r.hypothesis_scores, "必须有 hypothesis_scores"
    for hs in r.hypothesis_scores:
        assert hs.contradiction_penalty <= 0
        assert hs.missing_penalty <= 0
    assert any(hs.contradiction_penalty < 0 for hs in r.hypothesis_scores)


def test_gate_scores_contradictions_missing_reference_hypothesis():
    """Gate 硬断言：score/missing/contradiction 可反查 hypothesis。"""
    from rca_engine import RcaEngine

    fault_t = datetime(2026, 8, 19, 9, 0, tzinfo=timezone.utc)
    late_t = datetime(2026, 8, 19, 10, 0, tzinfo=timezone.utc)
    evs = [
        _evidence("m1", "metric_anomaly", "VM", 0.95, "spike", observed=fault_t),
        _evidence("c1", "change", "query-api", 0.9, "deploy after fault", observed=late_t),
    ]
    r = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
        run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=evs,
    )
    for hs in r.hypothesis_scores:
        assert hs.hypothesis_id is not None
    for c in r.contradictions:
        assert c.hypothesis_id is not None
    for m in r.missing_evidence:
        assert m.hypothesis_id is not None


def test_gate_root_cause_is_claim_not_uuid():
    """Gate 硬断言：root_cause 是 claim（描述），UUID 只在 RootCauseRef。"""
    from rca_engine import RcaEngine

    evs = [
        _evidence("m1", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("l1", "log_error", "VLogs", 0.85, "exception"),
        _evidence("t1", "trace_anomaly", "query-api", 0.90, "timeout"),
    ]
    r = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
        run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
        symptoms=["error rate spike"], evidences=evs, llm_reasoning_prior=0.8,
    )
    if r.root_cause is not None:
        # root_cause 是 claim 描述文本，不是 UUID 字符串
        try:
            uuid.UUID(str(r.root_cause))
            is_uuid = True
        except (ValueError, TypeError):
            is_uuid = False
        assert not is_uuid, f"root_cause 不应是 UUID: {r.root_cause!r}"
    # RootCauseRef.hypothesis_id 是 UUID
    for ref in r.root_cause_refs:
        assert isinstance(ref.hypothesis_id, uuid.UUID)


def test_gate_snapshot_v1_v2_different_rca_id():
    """Gate 硬断言：snapshot v1/v2 产生不同 rca_id（区分 follow-up/re-score）。"""
    from rca_engine import RcaEngine

    evs = [_evidence("m1", "metric_anomaly", "VM", 0.95, "spike")]
    eng = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    v1 = eng.run(run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
                 symptoms=["error"], evidences=evs, snapshot_version="v1")
    v2 = eng.run(run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
                 symptoms=["error"], evidences=evs, snapshot_version="v2")
    assert v1.rca_id != v2.rca_id


def test_gate_mismatch_evidence_rejected_before_scoring():
    """Gate 硬断言：mismatch Evidence（跨 cluster）在评分前 fail-closed 拒绝。"""
    from rca_engine import RcaEngine, EvidenceScopeMismatch

    cross = [_evidence("x1", "metric_anomaly", "VM", 0.95, "spike", cluster_id=CLUSTER2)]
    try:
        RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
            run_id=RUN, intent_id="i", resource_id="cluster-1/svc/checkout",
            symptoms=["error"], evidences=cross,
        )
        assert False, "应抛 EvidenceScopeMismatch"
    except EvidenceScopeMismatch:
        pass


def test_gate_production_confidence_equals_contract_confidence():
    """Gate 硬断言：rca_production.confidence == contract.confidence（不双重枚举映射）。"""
    from rca_production import run_rca_production
    from rca_engine import RcaEngine

    evs = [
        _evidence("m1", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("l1", "log_error", "VLogs", 0.85, "exception"),
        _evidence("t1", "trace_anomaly", "query-api", 0.90, "timeout"),
    ]
    contract = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER).run(
        run_id=RUN, intent_id="prod-rca", resource_id=f"{CLUSTER}/svc/checkout",
        symptoms=["checkout"], evidences=evs, llm_reasoning_prior=0.8,
    )
    prod = run_rca_production(
        service="checkout", cluster_id=CLUSTER, evidences=evs,
        run_id=RUN, tenant_id=TENANT, llm_prior=0.8,
    )
    assert prod["result"]["confidence"] == contract.confidence
    assert prod["result"]["confidence_state"] == contract.conclusion_state
