"""R2 Task 1 — ToolResult 收敛测试（2026-08-21 方案 B 修订）。

评审阻断项 2/3/4 修正后：
- contracts.ToolResult 恢复 V1 冻结 15 字段（对齐 Python/TS），不含 V2 草案字段。
- tool_result.py.normalize_tool_result 当前返回平行 ToolResult（Task1 已撤销，
  未接权威）—— 本测试明确断言该事实，消除"返回权威模型"的虚假绿灯。
- ACL to_contract 产出权威 V1 ToolResult；evidence_ids 只接受 UUID 或经
  fingerprint_index 解析到的已存在实体，未知 legacy ID fail-closed。
- UUIDv5(FROZEN_NAMESPACE, fingerprint) 黄金向量跨语言一致。
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from contracts import ToolResult as ContractToolResult
from contracts_identity import (
    FROZEN_EVIDENCE_NAMESPACE,
    canonical_provenance_fields,
    evidence_id_from_fingerprint,
    golden_vector,
    provenance_fingerprint,
    resolve_evidence_id,
)


def _now():
    return datetime.now(timezone.utc)


def _uuid(c):
    return "91771a6e-9c2d-11f1-8271-bea176fe9f9f" if c else c


def _t(c="91771a6e-9c2d-11f1-8271-bea176fe9f9f"):
    return c


# ── V1 冻结 ToolResult ──────────────────────────────────────────────────

def test_v1_tool_result_frozen_15_fields():
    """权威 ToolResult 保持 V1 冻结 15 字段（Python/TS 对齐），不含 V2 草案字段。

    Bugbot B4 修正：Python/TS 均为 15 字段；Go binding 多一个 `Error *StructuredError`
    （StructuredError 载体，wire 上应为 null 或省略），Go 侧 omitempty 对齐属 Task5 生成 Gate。
    """
    import contracts
    v2_fields = {
        "tenant_id", "tool_id", "request_id", "retry_policy",
        "evidence_required", "duration_ms", "provenance", "partial_reason", "denied_scope",
    }
    tr = contracts.ToolResult(
        tool_name="query_logs", cluster_id=_uuid("c"), success=True, status="success",
        summary="ok", source_system="VLogs", started_at=_now(), finished_at=_now(),
    )
    assert isinstance(tr, ContractToolResult)
    fields = set(ContractToolResult.model_fields.keys())
    assert fields & v2_fields == set(), (
        f"V1 冻结 ToolResult 不得包含 V2 草案字段，发现: {fields & v2_fields}"
    )
    v1_fields = ("tool_name", "cluster_id", "success", "status", "summary", "data",
                 "error_code", "error_message", "retryable", "evidence_ids",
                 "source_system", "query_id", "time_range", "started_at", "finished_at")
    assert fields == set(v1_fields), f"V1 冻结字段集合不匹配，发现: {fields ^ set(v1_fields)}"
    assert len(v1_fields) == 15


def test_v1_success_status_semantics_preserved():
    """V1 wire：success+status 联合语义保持。"""
    import contracts
    tr = contracts.ToolResult(
        tool_name="k8sgpt", cluster_id=_uuid("c"), success=True, status="no_data",
        summary="no diagnostics", source_system="k8sgpt", started_at=_now(), finished_at=_now(),
    )
    assert tr.status == "no_data"
    assert tr.success is True


# ── Task1 已撤销：normalize 返回平行（消除虚假绿灯）─────────────────────

def test_normalize_returns_parallel_not_contract():
    """Task1 已撤销：normalize_tool_result 返回平行 ToolResult，而非权威 contracts.ToolResult。

    该断言防止"声称已收敛但实际未接权威"的虚假绿灯。
    """
    from internal_query_client import QueryResult
    from tool_registry import ToolRegistry, init_default_tool_registry
    from tool_result import normalize_tool_result
    from tool_result import ToolResult as ParallelToolResult

    ToolRegistry._tools.clear()
    init_default_tool_registry()
    res = normalize_tool_result(
        outcome=QueryResult(200, {"logs": [{"ts": 1}], "total": 1}),
        tool=ToolRegistry.get("query_logs.v1"),
        tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        source_system="VLogs", request_id="r", query_id="q", time_range="a/b",
        started_at=_now(), finished_at=_now(),
    )
    assert isinstance(res, ParallelToolResult), "normalize 应返回平行 ToolResult（Task1 已撤销）"
    assert not isinstance(res, ContractToolResult), "normalize 不应返回权威 ToolResult"


# ── ACL to_contract → 权威 V1 ToolResult ─────────────────────────────────

def _parallel_toolresult(**over):
    from tool_result import ToolResult
    kw = dict(
        tool_name="query_logs", tool_id="query_logs.v1",
        cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        status="success", summary="ok", data={"logs": []}, error_code="", error_message="",
        retryable=False, retry_policy={}, evidence_ids=[], evidence_required=False,
        source_system="VLogs", request_id="req-1", query_id="q1",
        time_range="2026-08-20T00:00:00Z/2026-08-20T01:00:00Z",
        started_at=_now(), finished_at=_now(), duration_ms=0, provenance={},
    )
    kw.update(over)
    return ToolResult(**kw)


def test_acl_to_contract_type_adapts():
    """ACL 把平行 ToolResult 转权威 V1 contracts.ToolResult（类型适配 + evidence_ids 解析）。"""
    import uuid
    from acl.tool_result_ac import to_contract
    c = to_contract(_parallel_toolresult())
    assert isinstance(c, ContractToolResult)
    assert isinstance(c.cluster_id, uuid.UUID)
    assert isinstance(c.time_range, dict)
    assert c.status == "success"
    # V1 权威模型无 V2 草案字段
    assert "tenant_id" not in ContractToolResult.model_fields


def test_acl_resolves_evidence_ids_via_fingerprint_index():
    """非 UUID legacy 证据引用经 fingerprint_index 解析到已存在实体；否则拒绝（阻断项 4 + B3）。"""
    from acl.tool_result_ac import to_contract, ToolResultAcError
    from contracts_identity import evidence_id_from_fingerprint

    # 构造已存在实体：fingerprint → evidence_id(UUID)，且该 UUID 在 existing_ids
    fp = provenance_fingerprint("legacy-fp-1")
    eid = str(evidence_id_from_fingerprint(fp))
    idx = {fp: eid}
    existing = [eid]

    # 可解析且实体存在 → 转成 UUID
    c = to_contract(_parallel_toolresult(evidence_ids=[fp]),
                    fingerprint_index=idx, existing_ids=existing)
    assert str(c.evidence_ids[0]) == eid

    # 未知 legacy ID，无 fingerprint_index / 无 existing_ids → fail-closed
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(evidence_ids=["ev-unknown"]))

    # 未知 legacy ID，有 index 但实体不存在 → fail-closed
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(evidence_ids=["ev-nope"]),
                    fingerprint_index={}, existing_ids=existing)

    # 可解析但实体不在 existing_ids → fail-closed（防悬空引用）
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(evidence_ids=[fp]),
                    fingerprint_index=idx, existing_ids=[])


def test_acl_accepts_existing_uuid_evidence_ids():
    """已是 UUID 的 evidence_id：实体存在才通过；否则拒绝（B3）。"""
    from acl.tool_result_ac import to_contract, ToolResultAcError
    eid = "99999999-9999-4999-8999-999999999999"
    # 实体存在 → 通过
    c = to_contract(_parallel_toolresult(evidence_ids=[eid]), existing_ids=[eid])
    assert str(c.evidence_ids[0]) == eid
    # UUID 但实体不存在 / 未提供存在集合 → 拒绝
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(evidence_ids=[eid]), existing_ids=[])
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(evidence_ids=[eid]))


def test_acl_rejects_non_canonical_cluster_id():
    """ACL 拒绝非法（非 canonical UUID）cluster_id，fail-closed。"""
    from acl.tool_result_ac import to_contract, ToolResultAcError
    with pytest.raises(ToolResultAcError):
        to_contract(_parallel_toolresult(cluster_id="default"))


# ── UUIDv5 黄金向量（跨语言一致，阻断项 3）──────────────────────────────

def test_uuidv5_namespace_frozen():
    """FROZEN_EVIDENCE_NAMESPACE 固定，不得漂移。"""
    assert str(FROZEN_EVIDENCE_NAMESPACE) == "6f1c3a5e-2b8a-4f3e-9d2c-1a0b3c4d5e6f"


def test_golden_vector_fingerprint_deterministic():
    """同一规范字段 → 同一 fingerprint；不同隔离维度 → 不同。"""
    v = golden_vector({
        "source": "VM", "query_id": "qry-1", "resource_id": "svc:orders",
        "time_range_start": "2026-08-19T09:00:00Z",
        "time_range_end": "2026-08-19T10:00:00Z",
        "digest": "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
        "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        "run_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    })
    assert v == provenance_fingerprint(
        canonical_provenance_fields(
            source="VM", query_id="qry-1", resource_id="svc:orders",
            time_range_start="2026-08-19T09:00:00Z",
            time_range_end="2026-08-19T10:00:00Z",
            digest="aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            run_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        )
    )
    assert len(v) == 64
    assert v == v.lower()


def test_golden_vector_evidence_id_uuidv5():
    """UUIDv5(FROZEN_NS, fingerprint) 确定且为合法 UUID。"""
    import uuid as _uuid
    fp = provenance_fingerprint("svc:orders-vm")
    eid = evidence_id_from_fingerprint(fp)
    assert isinstance(eid, _uuid.UUID)
    assert evidence_id_from_fingerprint(fp) == eid  # 确定性


def test_resolve_evidence_id_fail_closed_unknown():
    """未知 legacy ID：无实体解析即拒绝，禁止无条件 UUIDv5 化制造悬空引用。"""
    with pytest.raises(ValueError):
        resolve_evidence_id("ev-unknown")
    with pytest.raises(ValueError):
        resolve_evidence_id(None)
    with pytest.raises(ValueError):
        resolve_evidence_id("")


def test_naive_time_rejected():
    """naive（无时区）时间拒绝（C2）：不依赖机器本地时区，防跨环境 Evidence ID 漂移。"""
    from datetime import datetime
    from contracts_identity import normalize_iso, canonical_provenance_fields
    # naive datetime 拒绝
    with pytest.raises(ValueError):
        normalize_iso(datetime(2026, 8, 19, 9, 0, 0))
    # naive ISO 字符串拒绝
    with pytest.raises(ValueError):
        normalize_iso("2026-08-19T09:00:00")
    # canonical 中带 naive 时间拒绝
    with pytest.raises(ValueError):
        canonical_provenance_fields(
            source="VM", query_id="q", resource_id="r",
            time_range_start="2026-08-19T09:00:00", time_range_end="2026-08-19T10:00:00Z",
            digest="aa", tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            run_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        )


def test_time_zone_offsets_normalize_same_instant():
    """同一时刻的不同时区表示归一为同一 canonical（C2/B2）。"""
    from contracts_identity import normalize_iso
    assert normalize_iso("2026-08-19T09:00:00Z") == "2026-08-19T09:00:00Z"
    assert normalize_iso("2026-08-19T17:00:00+08:00") == "2026-08-19T09:00:00Z"
    assert normalize_iso("2026-08-19T09:00:00.123Z") == "2026-08-19T09:00:00Z"


def test_illegal_source_rejected():
    """非法 source（LLM/Agent）拒绝，不伪装 query-api（P0-3 延续）。"""
    from internal_query_client import QueryResult
    from tool_result import normalize_tool_result
    from tool_registry import ToolRegistry, init_default_tool_registry

    ToolRegistry._tools.clear()
    init_default_tool_registry()
    with pytest.raises(ValueError):
        normalize_tool_result(
            outcome=QueryResult(200, {"logs": [{"ts": 1}], "total": 1}),
            tool=ToolRegistry.get("query_logs.v1"),
            tenant_id="t1", cluster_id="c1", source_system="LLM",
            request_id="r", query_id="q", time_range="a/b",
            started_at=_now(), finished_at=_now(),
        )
