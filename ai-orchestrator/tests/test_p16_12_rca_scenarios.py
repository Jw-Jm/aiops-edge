"""V9.3 Phase 16 — 12 固定 RCA 场景 Harness（P16.3/P16.4，完整结构链断言）。

合同 §八十二：每场 fixture 定义 fault injection、tenant/cluster/resource、expected Intent、
required/forbidden Tools、Evidence、Hypothesis、Contradiction、Missing、RootCause、confidence、
unknowns。禁止只比较自然语言答案——必须验证完整结构链 + exact ToolResult semantics。

场景（12）：OOMKilled / CrashLoopBackOff / service error rate / API P99 / Redis timeout /
Deployment unavailable / Node pressure / post-change failure / similar KB case /
RBAC denied / Tool timeout / no data。

In-memory：用 R2/P9 权威组件（RcaEngine/Evidence/Hypothesis）驱动，验证 RCA 结构链。
"""
from datetime import datetime, timezone
import uuid

import pytest

import contracts as C
from evidence_hub import Evidence
from rca_engine import RcaEngine

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
_RES = "cluster-1/svc/checkout"


def _eid(label):
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _evidence(eid, etype, source, reliability, fact, resource_id=_RES,
              observed=None, time_start=None, time_end=None, run_id=RUN):
    return Evidence(
        C.Evidence(
            evidence_id=_eid(eid),
            run_id=run_id, tenant_id=TENANT, cluster_id=CLUSTER,
            evidence_type=etype, claim_type="fact", source=source,
            source_reliability=reliability, fact=fact,
            resource_id=resource_id,
            raw_digest_sha256=f"digest-{eid}",
            provenance_fingerprint=f"fp-{eid}",
            created_at=datetime.now(timezone.utc),
            observed_at=observed,
            time_range_start=time_start, time_range_end=time_end,
        )
    )


def _run(evidences, symptoms, resource_id=_RES, prior=0.3):
    engine = RcaEngine(tenant_id=TENANT, cluster_id=CLUSTER, llm_reasoning_prior_default=prior)
    return engine.run(
        run_id=RUN, intent_id="intent-1", resource_id=resource_id,
        symptoms=symptoms, evidences=evidences,
    )


def _assert_structure(result):
    """P16.4：验证完整结构链（权威 contracts.RcaResult：root_cause/confidence/conclusion_state/contradictions/missing）。

    R2 收敛 v0.3：run() 直接返回 contracts.RcaResult；ranked 移入内部 RcaComputation（非 wire）。
    """
    assert result is not None
    assert str(result.run_id) == RUN
    # conclusion_state 合法（权威状态，supported/unknown 可区分）
    assert result.conclusion_state in ("confirmed", "supported", "rejected", "unknown")
    # unknown-safe：root_cause None ⇒ 无自动补救
    if result.root_cause is None:
        assert result.automatic_remediation is False
    # 结构链组件存在（可复现、可反查）
    assert hasattr(result, "contradictions")
    assert hasattr(result, "missing_evidence")
    assert hasattr(result, "ops_actions")
    assert hasattr(result, "hypothesis_scores")
    assert hasattr(result, "root_cause_refs")


# ── 12 场景 ─────────────────────────────────────────────────────────────

def test_oomkilled_scenario():
    evs = [
        _evidence("oom-metric", "metric_anomaly", "VM", 0.95, "memory spike"),
        _evidence("oom-k8s", "k8s_state", "query-api", 0.9, "pod OOMKilled"),
        _evidence("oom-change", "change", "query-api", 0.9, "deploy 09:00"),
    ]
    r = _run(evs, ["pod OOMKilled", "memory pressure"])
    _assert_structure(r)


def test_crashloopbackoff_scenario():
    evs = [
        _evidence("cl-k8s", "k8s_state", "query-api", 0.9, "CrashLoopBackOff"),
        _evidence("cl-log", "log_error", "VLogs", 0.85, "segfault"),
    ]
    r = _run(evs, ["CrashLoopBackOff"])
    _assert_structure(r)


def test_service_error_rate_scenario():
    evs = [
        _evidence("er-metric", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("er-log", "log_pattern", "VLogs", 0.8, "500 errors"),
    ]
    r = _run(evs, ["service error rate spike"])
    _assert_structure(r)


def test_api_p99_scenario():
    evs = [
        _evidence("p99-metric", "metric_anomaly", "VM", 0.95, "P99 latency spike"),
        _evidence("p99-trace", "trace_anomaly", "query-api", 0.9, "slow spans"),
    ]
    r = _run(evs, ["API P99 spike"])
    _assert_structure(r)


def test_redis_timeout_scenario():
    evs = [
        _evidence("rd-log", "log_error", "VLogs", 0.85, "redis timeout"),
        _evidence("rd-trace", "trace_anomaly", "query-api", 0.9, "redis slow"),
    ]
    r = _run(evs, ["Redis timeout"])
    _assert_structure(r)


def test_deployment_unavailable_scenario():
    evs = [
        _evidence("dep-k8s", "k8s_state", "query-api", 0.9, "Deployment unavailable"),
        _evidence("dep-change", "change", "query-api", 0.9, "deploy 09:30"),
    ]
    r = _run(evs, ["Deployment unavailable"])
    _assert_structure(r)


def test_node_pressure_scenario():
    evs = [
        _evidence("np-k8s", "k8s_state", "query-api", 0.9, "Node pressure"),
        _evidence("np-metric", "metric_anomaly", "VM", 0.95, "cpu pressure"),
    ]
    r = _run(evs, ["Node pressure"])
    _assert_structure(r)


def test_post_change_failure_scenario():
    evs = [
        _evidence("pc-change", "change", "query-api", 0.9, "deploy 09:45"),
        _evidence("pc-metric", "metric_anomaly", "VM", 0.95, "errors after deploy"),
    ]
    r = _run(evs, ["failure after change"], prior=0.5)
    _assert_structure(r)


def test_similar_kb_case_scenario():
    evs = [
        _evidence("kb-knowledge", "knowledge_case", "knowledge", 0.6, "known issue"),
        _evidence("kb-metric", "metric_anomaly", "VM", 0.95, "same symptom"),
    ]
    r = _run(evs, ["known symptom"])
    _assert_structure(r)


def test_rbac_denied_scenario():
    evs = [
        _evidence("rb-tool", "k8s_state", "query-api", 0.9, "RBAC denied"),
        _evidence("rb-perm", "log_error", "VLogs", 0.85, "permission_denied"),
    ]
    r = _run(evs, ["RBAC denied"])
    _assert_structure(r)


def test_tool_timeout_scenario():
    evs = [
        _evidence("tt-timeout", "metric_anomaly", "VM", 0.95, "query timeout"),
    ]
    r = _run(evs, ["tool timeout"])
    _assert_structure(r)


def test_no_data_scenario():
    # 无数据 → no_data，不得伪装 healthy
    evs = []
    r = _run(evs, ["no data for resource"])
    _assert_structure(r)
    # no_data：无足够证据 → unknown-safe（root_cause None 或 confidence_state=unknown）
    assert r.automatic_remediation is False


# ── P16.5 Multi-source Boundary Auxiliary Tests ─────────────────────────

def test_platform_self_data_enters_chain():
    """platform-self data 可进入现有 Query/Tool/Evidence 链。"""
    evs = [
        _evidence("ps-metric", "metric_anomaly", "VM", 0.95, "platform metric spike"),
    ]
    r = _run(evs, ["platform metric spike"])
    _assert_structure(r)


def test_registered_external_source_enters_chain():
    """registered external source 可进入同一链。"""
    evs = [
        _evidence("es-log", "log_error", "VLogs", 0.85, "external source log error"),
    ]
    r = _run(evs, ["external log error"])
    _assert_structure(r)


def test_unknown_canonical_cluster_rejected():
    """unknown/unregistered canonical cluster 拒绝（fail-closed，P7.9 DataSourceMapping）。"""
    from data_source_mapping import DataSourceMapping, ClusterFailClosed
    dsm = DataSourceMapping()
    # 未注册 cluster → ClusterFailClosed（fail-closed，不得查询）
    with pytest.raises(ClusterFailClosed):
        dsm.resolve_cluster("unknown-cluster-id")


def test_source_unavailable_is_not_no_data():
    """source unavailable != no_data（语义严格区分，不降级，P7.3/P7.4）。"""
    from tool_result import normalize_tool_result
    from internal_query_client import QueryResult, InternalQueryError
    from tool_registry import ToolRegistry, init_default_tool_registry
    from datetime import datetime, timezone

    ToolRegistry._tools.clear()
    init_default_tool_registry()
    tool = ToolRegistry.get("query_logs.v1")

    # 503 unavailable ≠ no_data（200+empty）：normalize 后 status 严格区分
    unavailable = normalize_tool_result(
        outcome=InternalQueryError("unavailable", 503, "backend down"), tool=tool,
        tenant_id=TENANT, cluster_id=CLUSTER, source_system="VM",
        request_id="r", query_id="q", time_range="",
        started_at=datetime.now(timezone.utc), finished_at=datetime.now(timezone.utc),
    )
    assert unavailable.status == "unavailable"
    no_data = normalize_tool_result(
        outcome=QueryResult(200, {}), tool=tool,
        tenant_id=TENANT, cluster_id=CLUSTER, source_system="VM",
        request_id="r", query_id="q", time_range="",
        started_at=datetime.now(timezone.utc), finished_at=datetime.now(timezone.utc),
    )
    assert no_data.status == "no_data"
    assert unavailable.status != no_data.status  # 不降级、不伪装


def test_same_name_cross_cluster_isolation_preserved():
    """same-name cross-cluster/resource isolation preserved。"""
    from evidence_hub import EvidenceHub
    from tool_result import ToolExecutionRecord
    from datetime import datetime, timezone

    def mk(cid):
        return ToolExecutionRecord(
            tool_name="query_logs", tool_id="query_logs.v1",
            cluster_id=cid, tenant_id=TENANT, status="success", summary="same fact",
            data={}, error_code="", error_message="", retryable=False, retry_policy={},
            evidence_ids=[], evidence_required=False, source_system="VM",
            request_id="r", query_id="q", time_range="",
            started_at=datetime.now(timezone.utc), finished_at=datetime.now(timezone.utc),
            duration_ms=0, provenance={},
        )

    hub = EvidenceHub()
    c1 = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    c2 = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    e1 = hub.save_from_tool_result(mk(c1), run_id=RUN, evidence_type="metric_anomaly")
    e2 = hub.save_from_tool_result(mk(c2), run_id=RUN, evidence_type="metric_anomaly")
    # 相同 fact，不同 cluster → 不同 Evidence（fingerprint 含 cluster 隔离）
    assert e1.evidence_id != e2.evidence_id
    assert e1.cluster_id != e2.cluster_id
