"""P9.5 Missing Evidence — 主动推导（评审加固测试）。

评审修复：MissingEvidenceEngine 必须能从 Hypothesis.required_support vs 实际
Evidence 类型推导缺失类别，而非仅手工 add。
"""
import uuid
from datetime import datetime, timezone

import contracts as C

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
_H = "5c5bdbb2-8f0d-5b0e-a2e1-9a1b2c3d4e5f"


def _eid(label):
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _evidence(eid, etype):
    from evidence_hub import Evidence

    return Evidence(
        C.Evidence(
            evidence_id=_eid(eid),
            run_id=RUN, tenant_id=TENANT, cluster_id=CLUSTER,
            evidence_type=etype, claim_type="fact", source="VM",
            source_reliability=0.9, fact=eid,
            raw_digest_sha256=f"digest-{eid}",
            provenance_fingerprint=f"fp-{eid}",
            created_at=datetime.now(timezone.utc),
        )
    )


def _hypothesis(required_support):
    from hypothesis import Hypothesis

    return Hypothesis(
        C.Hypothesis(
            hypothesis_id=_H, run_id=RUN, title="c", description="m",
            confidence=0.0, status=C.HypothesisStatus.CANDIDATE,
            tenant_id=TENANT, cluster_id=CLUSTER, resource_id="cluster-1/svc",
            affected_resource="r",
        ),
        required_support=required_support,
    )


def test_derive_missing_from_required_support():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    h = _hypothesis(required_support=["metric_anomaly", "trace_anomaly"])
    # 只有 metric，缺 trace
    evs = [_evidence("ev-m", "metric_anomaly")]
    missing = eng.derive(h, evs)
    assert any(m.required_type == "trace_anomaly" for m in missing)
    assert not any(m.required_type == "metric_anomaly" for m in missing)


def test_derive_no_missing_when_all_required_present():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    h = _hypothesis(required_support=["metric_anomaly"])
    evs = [_evidence("ev-m", "metric_anomaly")]
    missing = eng.derive(h, evs)
    assert len(missing) == 0


def test_derive_marks_required_as_critical():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    h = _hypothesis(required_support=["log_error", "trace_anomaly"])
    evs = [_evidence("ev-l", "log_error")]
    missing = eng.derive(h, evs)
    trace_missing = next(m for m in missing if m.required_type == "trace_anomaly")
    # required support 缺失 → critical（限制最终状态）
    assert trace_missing.critical is True


def test_derive_reason_is_insufficient_data():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    h = _hypothesis(required_support=["k8s_state"])
    missing = eng.derive(h, [])
    assert missing[0].reason == "insufficient_data"


def test_derive_populates_engine():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    h = _hypothesis(required_support=["metric_anomaly", "trace_anomaly"])
    evs = [_evidence("ev-m", "metric_anomaly")]
    eng.derive(h, evs)
    assert any(m.required_type == "trace_anomaly" for m in eng.critical_missing(_H))
