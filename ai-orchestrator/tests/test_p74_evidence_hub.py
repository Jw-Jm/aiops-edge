"""P7.4 Evidence Hub — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.4 设计的 T1-T6：
- T1 归一化与落库（ToolResult→Evidence normalize）
- T2 去重（相同 provenance_fingerprint → 复用，不重复计分）
- T3 生命周期（created→validated→expired→archived，旧证据≠当前事实）
- T4 不可变 + LLM 红线（metadata/fingerprint/digest 不可变；LLM inference 拒绝；inference 引 supporting evidence）
- T5 关联与追溯（run_id/evidence_id；source 禁 AI；unknown 记录 reason）
- T6 Freshness Isolation（expired/archived 不作 current fact；引用 expired → stale）
"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import UUID

import pytest

from evidence_hub import (
    Evidence,
    EvidenceHub,
)
from tool_registry import ToolRegistry, init_default_tool_registry


def _reset_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    init_default_tool_registry()


@pytest.fixture(autouse=True)
def _fresh():
    _reset_registry()
    yield
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()


TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
REQ_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
QUERY_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
TIME_RANGE = "2026-08-20T00:00:00Z/2026-08-20T01:00:00Z"


def _now():
    return datetime.now(timezone.utc)


def _make_tool_result(**over):
    from tool_result import ToolResult

    kw = dict(
        tool_name="query_logs", tool_id="query_logs.v1", cluster_id=CLUSTER, tenant_id=TENANT,
        status="success", summary="查询到 3 条日志", data={"logs": [{"ts": 1}, {"ts": 2}, {"ts": 3}], "total": 3},
        error_code="", error_message="", retryable=False, retry_policy={"max_attempts": 0, "backoff": 1.0},
        evidence_ids=[], evidence_required=True, source_system="query-api",
        request_id=REQ_ID, query_id=QUERY_ID, time_range=TIME_RANGE,
        started_at=_now(), finished_at=_now(), duration_ms=5,
        provenance={"tool_id": "query_logs.v1", "request_id": REQ_ID, "trusted_context_id": QUERY_ID,
                    "source_timestamp": _now().isoformat()},
    )
    kw.update(over)
    return ToolResult(**kw)


@pytest.fixture
def hub():
    return EvidenceHub()


# ═══════════════════════════════════════════════════════
#  T1 归一化与落库
# ═══════════════════════════════════════════════════════

class TestT1NormalizeAndStore:
    def test_save_from_tool_result_creates_evidence(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(),
            run_id="22222222-2222-4222-8222-222222222222",
            evidence_type="log_pattern",
        )
        assert ev.evidence_id
        assert ev.source == "query-api"
        assert ev.claim_type == "fact"
        assert ev.status == "created"
        assert ev.tenant_id == TENANT
        assert ev.cluster_id == CLUSTER
        assert ev.fact == "查询到 3 条日志"
        assert ev.raw_digest_sha256

    def test_evidence_type_valid(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(),
            run_id="22222222-2222-4222-8222-222222222222",
            evidence_type="log_error",
        )
        assert ev.evidence_type == "log_error"


# ═══════════════════════════════════════════════════════
#  T2 去重（§三十五）
# ═══════════════════════════════════════════════════════

class TestT2Dedup:
    def test_same_fingerprint_reuses_evidence_id(self, hub):
        ev1 = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        ev2 = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        # 相同事实（同 source/query/resource/digest）→ 复用同一 evidence_id，不新建
        assert ev1.evidence_id == ev2.evidence_id
        assert len(hub.all()) == 1

    def test_different_fact_new_evidence(self, hub):
        ev1 = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        ev2 = hub.save_from_tool_result(
            _make_tool_result(data={"logs": [{"ts": 9}], "total": 1}, summary="不同事实"),
            run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern",
        )
        assert ev1.evidence_id != ev2.evidence_id


# ═══════════════════════════════════════════════════════
#  T3 生命周期
# ═══════════════════════════════════════════════════════

class TestT3Lifecycle:
    def test_created_to_validated_to_expired_to_archived(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        ev = hub.transition(ev.evidence_id, "validated")
        assert ev.status == "validated"
        ev = hub.transition(ev.evidence_id, "expired")
        assert ev.status == "expired"
        ev = hub.transition(ev.evidence_id, "archived")
        assert ev.status == "archived"

    def test_expired_not_current_fact(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        hub.transition(ev.evidence_id, "validated")
        assert len(hub.current_facts()) == 1
        hub.transition(ev.evidence_id, "expired")
        assert hub.current_facts() == []  # 旧证据 ≠ 当前事实


# ═══════════════════════════════════════════════════════
#  T4 不可变 + LLM 红线
# ═══════════════════════════════════════════════════════

class TestT4ImmutableAndLLM:
    def test_metadata_immutable(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        with pytest.raises(Exception):
            ev.metadata["hack"] = True  # frozen dataclass → 拒绝

    def test_llm_inference_rejected(self, hub):
        # R2 收敛：Evidence 封装权威 contracts.Evidence；source="LLM" 在封装 __init__ 拒绝
        import contracts as C
        with pytest.raises(ValueError):
            Evidence(
                C.Evidence(
                    evidence_id=UUID("99999999-9999-4999-8999-999999999991"),
                    run_id=UUID("22222222-2222-4222-8222-222222222222"),
                    tenant_id=TENANT, cluster_id=CLUSTER, evidence_type="log_pattern",
                    claim_type="inference", source="LLM", source_reliability=0.0,
                    fact="LLM 猜测根因", raw_digest_sha256="x",
                    provenance_fingerprint="fp-llm", created_at=_now(),
                    metadata={"supporting_evidence_ids": []},
                ),
                supporting_evidence=[],
            )

    def test_inference_requires_supporting_evidence(self, hub):
        with pytest.raises(ValueError):
            hub.save_from_tool_result(
                _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222",
                evidence_type="log_pattern", claim_type="inference", supporting_evidence=[],
            )


# ═══════════════════════════════════════════════════════
#  T5 关联与追溯
# ═══════════════════════════════════════════════════════

class TestT5RelationAndTrace:
    def test_run_id_evidence_id_relation(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        assert ev.run_id == "22222222-2222-4222-8222-222222222222"
        assert hub.get(ev.evidence_id).evidence_id == ev.evidence_id

    def test_unknown_records_reason(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(summary="证据不足"), run_id="22222222-2222-4222-8222-222222222222",
            evidence_type="log_pattern", claim_type="unknown", unknown_reason="insufficient_data",
        )
        assert ev.claim_type == "unknown"
        assert ev.unknown_reason == "insufficient_data"


# ═══════════════════════════════════════════════════════
#  T6 Freshness Isolation
# ═══════════════════════════════════════════════════════

class TestT6FreshnessIsolation:
    def test_archived_not_current_fact(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        hub.transition(ev.evidence_id, "validated")
        hub.transition(ev.evidence_id, "archived")
        assert hub.current_facts() == []
        assert hub.get(ev.evidence_id).status == "archived"  # 审计保留

    def test_reference_to_expired_marked_stale(self, hub):
        ev = hub.save_from_tool_result(
            _make_tool_result(), run_id="22222222-2222-4222-8222-222222222222", evidence_type="log_pattern"
        )
        hub.transition(ev.evidence_id, "expired")
        assert hub.reference_status(ev.evidence_id) == "stale"
