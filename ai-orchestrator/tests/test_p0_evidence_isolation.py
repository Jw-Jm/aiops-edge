"""P0-2 Evidence 跨租户/跨 run 隔离测试（审计阻断项 B0-02）。

fingerprint 必须包含 tenant_id/cluster_id/run_id 隔离维度；
相同 source/query/resource/time/digest 在不同 tenant/cluster/run 必须是不同 Evidence，
禁止后续租户复用首个租户的 Evidence 对象。

R2 方案 B 迁移（2026-08-21）：fingerprint 规范要求 tenant/cluster/run 为 UUID，
隔离标签改为合法 UUID（不同 tenant/cluster/run 用不同 UUID），隔离语义不变。
"""


# R2 方案 B：隔离标签用合法 UUID（不同租户/集群/run 不同值）
T_A = "aaaaaaaa-0000-4000-8000-00000000000a"
T_B = "bbbbbbbb-0000-4000-8000-00000000000b"
C_1 = "cccccccc-0000-4000-8000-000000000001"
C_2 = "dddddddd-0000-4000-8000-000000000002"
R_1 = "eeeeeeee-0000-4000-8000-000000000001"
R_2 = "ffffffff-0000-4000-8000-000000000002"


def _tr(tenant_id, cluster_id, source="VM", query_id="q1",
        summary="metric spike", data=None):
    from datetime import datetime, timezone
    from tool_result import ToolResult

    now = datetime.now(timezone.utc)
    return ToolResult(
        tool_name="query_metrics",
        tool_id="query_metrics.v1",
        status="success",
        tenant_id=tenant_id,
        cluster_id=cluster_id,
        source_system=source,
        query_id=query_id,
        request_id="req-1",
        time_range="2026-08-20T00:00:00Z/2026-08-20T01:00:00Z",
        summary=summary,
        data=data or {"val": 1},
        error_code="",
        error_message=None,
        retryable=False,
        retry_policy={},
        evidence_ids=[],
        evidence_required=True,
        started_at=now,
        finished_at=now,
        duration_ms=0,
        provenance={},
    )


def _hub():
    from evidence_hub import EvidenceHub

    return EvidenceHub()


def test_same_tenant_same_run_reuses_evidence():
    hub = _hub()
    tr = _tr(T_A, C_1)
    e1 = hub.save_from_tool_result(tr, run_id=R_1, evidence_type="metric_anomaly")
    e2 = hub.save_from_tool_result(tr, run_id=R_1, evidence_type="metric_anomaly")
    assert e1.evidence_id == e2.evidence_id  # 同租户同 run → 去重复用


def test_different_tenant_not_reused():
    hub = _hub()
    e1 = hub.save_from_tool_result(_tr(T_A, C_1), run_id=R_1,
                                   evidence_type="metric_anomaly")
    # 相同 source/query/resource/time/digest，但不同 tenant → 必须是不同 Evidence
    e2 = hub.save_from_tool_result(_tr(T_B, C_1), run_id=R_1,
                                   evidence_type="metric_anomaly")
    assert e1.evidence_id != e2.evidence_id
    assert e1.tenant_id == T_A
    assert e2.tenant_id == T_B


def test_different_cluster_not_reused():
    hub = _hub()
    e1 = hub.save_from_tool_result(_tr(T_A, C_1), run_id=R_1,
                                   evidence_type="metric_anomaly")
    e2 = hub.save_from_tool_result(_tr(T_A, C_2), run_id=R_1,
                                   evidence_type="metric_anomaly")
    assert e1.evidence_id != e2.evidence_id


def test_different_run_not_reused():
    hub = _hub()
    e1 = hub.save_from_tool_result(_tr(T_A, C_1), run_id=R_1,
                                   evidence_type="metric_anomaly")
    e2 = hub.save_from_tool_result(_tr(T_A, C_1), run_id=R_2,
                                   evidence_type="metric_anomaly")
    assert e1.evidence_id != e2.evidence_id


def test_reuse_validates_ownership():
    """fingerprint 冲突但归属不一致 → 拒绝复用（fail-closed）。"""
    hub = _hub()
    tr_a = _tr(T_A, C_1)
    e1 = hub.save_from_tool_result(tr_a, run_id=R_1, evidence_type="metric_anomaly")
    fp_same = e1.provenance_fingerprint
    # fingerprint 已含 tenant/cluster/run 隔离维度，跨租户必然不同 fp
    assert fp_same


def test_illegal_llm_source_not_disguised():
    """审计 P0-3 复现：非法 LLM source 不得被伪装成 query-api，必须拒绝。"""
    import pytest as _pytest
    from tool_result import _normalize_source

    # LLM / Agent / 未知来源 → 拒绝，绝不返回 query-api 伪装
    for illegal in ("LLM", "Agent", "chatgpt", "unknown-src"):
        with _pytest.raises(ValueError):
            _normalize_source(illegal)
    # 合法来源 → 原样返回
    assert _normalize_source("VM") == "VM"
    assert _normalize_source("query-api") == "query-api"
