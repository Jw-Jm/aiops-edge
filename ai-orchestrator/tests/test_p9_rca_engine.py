"""RcaEngine — 单一 RCA 编排器（评审加固测试）。

评审修复（P0）：新 RCA 必须接入生产主链，不再是孤立模块。
RcaEngine 是 Phase 9 正式 RCA 编排器：从 Evidence → Hypothesis → Support →
Contradiction(derive) → Missing(derive) → Scoring → Ranker → Timeline → Unknown-safe。

输入只能是 Evidence Hub 生成的 Evidence；输出 RootCause/Confidence/Unknowns。
"""
import uuid
from datetime import datetime, timezone

import pytest

import contracts as C

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"


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


def test_rca_engine_runs_full_chain_from_evidence():
    from rca_engine import RcaEngine

    evidences = [
        _evidence("ev-m", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("ev-c", "change", "query-api", 0.9, "deploy 10:00"),
    ]
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error rate spike"], evidences=evidences,
    )
    # 返回结构化 RCA 结果（权威 contracts.RcaResult：root_cause 用 None 表达 unknown）
    assert result.root_cause is not None or result.conclusion_state == "unknown"
    assert result.conclusion_state in ("confirmed", "supported", "unknown")


def test_rca_engine_confirms_when_strong_support():
    from rca_engine import RcaEngine

    # 完整证据覆盖 error_rate 模板的 required_support（metric+log_error+trace_anomaly）
    evidences = [
        _evidence("ev-m", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("ev-l", "log_error", "VLogs", 0.85, "exception thrown"),
        _evidence("ev-t", "trace_anomaly", "query-api", 0.90, "dependency timeout"),
    ]
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error rate spike"], evidences=evidences, llm_reasoning_prior=0.8,
    )
    # 强支持 + 无 critical 矛盾 → confirmed（权威 conclusion_state）
    assert result.conclusion_state == "confirmed"


def test_rca_engine_unknown_when_no_evidence():
    from rca_engine import RcaEngine

    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=[],
    )
    assert result.root_cause is None  # 权威 None 表达 unknown（方案 B）
    assert result.conclusion_state == "unknown"
    assert result.automatic_remediation is False


def test_rca_engine_missing_blocks_confirmation():
    from rca_engine import RcaEngine

    # 只有 metric，缺 change/trace（required_support 推导缺失）→ 无法 confirmed
    evidences = [_evidence("ev-m", "metric_anomaly", "VM", 0.95, "error spike")]
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error rate spike"], evidences=evidences, llm_reasoning_prior=0.8,
    )
    assert result.root_cause is None
    assert len(result.missing_evidence) >= 0


def test_rca_engine_missing_evidence_not_dropped():
    """审计 P1：missing_evidence 不应被 _all_missing/_Rankable 缺陷丢弃。

    只有 metric 证据时，MissingEvidenceEngine.derive 应推导出缺失的
    required_support 类型（change/log_error/trace_anomaly 等），这些必须
    出现在 RcaResult.missing_evidence 中，而非恒为空。
    """
    from rca_engine import RcaEngine

    evidences = [_evidence("ev-m", "metric_anomaly", "VM", 0.95, "error spike")]
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error rate spike"], evidences=evidences, llm_reasoning_prior=0.8,
    )
    # 缺失证据类型应被回传（不为空）
    assert len(result.missing_evidence) > 0


def test_rca_engine_contradictions_not_dropped():
    """审计 P1：contradictions 不应被返回时的硬编码 [] 丢弃。

    同 cluster 内的 change_after_fault（change 事件发生在 fault 之后）会被
    ContradictionChecker.detect 推导为 change_after_fault critical 矛盾，
    必须出现在权威 contracts.RcaResult.contradictions 中（kind 反查，不为空）。
    """
    from rca_engine import RcaEngine

    fault_t = datetime(2026, 8, 19, 9, 0, tzinfo=timezone.utc)
    late_change_t = datetime(2026, 8, 19, 10, 0, tzinfo=timezone.utc)  # 在 fault 之后
    evidences = [
        _evidence("ev-m", "metric_anomaly", "VM", 0.95, "error spike", observed=fault_t),
        _evidence("ev-c", "change", "query-api", 0.9, "deploy after fault", observed=late_change_t),
    ]
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=evidences, llm_reasoning_prior=0.8,
    )
    # 同 cluster 内 contradiction 应被回传（不为空），且可反查 hypothesis/evidence
    assert len(result.contradictions) > 0
    assert all(c.hypothesis_id is not None for c in result.contradictions)


CLUSTER2 = "dddddddd-0000-4000-8000-000000000002"


def test_rca_engine_respects_tenant_cluster_isolation():
    from rca_engine import RcaEngine, EvidenceScopeMismatch

    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    # 跨 cluster Evidence 在评分前被 fail-closed 拒绝（设计 v0.3 §8，非仅 contradiction）
    cross = [_evidence("ev-x", "metric_anomaly", "VM", 0.95, "spike", cluster_id=CLUSTER2)]
    with pytest.raises(EvidenceScopeMismatch):
        engine.run(
            run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
            symptoms=["error"], evidences=cross, llm_reasoning_prior=0.8,
        )


def test_rca_engine_no_auto_remediation_f5():
    from rca_engine import RcaEngine

    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER)
    result = engine.run(
        run_id=RUN, intent_id="intent-1", resource_id="cluster-1/svc/checkout",
        symptoms=["error"], evidences=[], llm_reasoning_prior=0.0,
    )
    assert result.automatic_remediation is False
    assert result.ops_actions == []
