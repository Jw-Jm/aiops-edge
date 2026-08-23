"""P7.3 ToolResult Normalization — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.3 设计的 T1-T6：
- T1 状态归一化（success/no_data/unavailable/permission_denied/timeout/failed/partial）
- T2 禁止降级（permission_denied→no_data / no_data→healthy / unavailable→healthy / 403→no_data / network→no_data）
- T3 重试与超时（permission_denied/no_data 不可重试；failed/timeout/unavailable 可重试受上限）
- T4 Evidence 兼容（evidence_ids / evidence_required / source_system 禁 AI）
- T5 Schema 完整（status 非法拒绝 / 缺必填字段拒绝）
- T6 Context Traceability（request_id / provenance / denied_scope / partial_reason / source_system 禁 AI）
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from internal_query_client import InternalQueryError, QueryResult
from tool_registry import ToolDefinition, ToolRegistry, init_default_tool_registry


# ═══════════════════════════════════════════════════════
#  Helpers
# ═══════════════════════════════════════════════════════

def _reset_registry():
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()
    init_default_tool_registry()


@pytest.fixture(autouse=True)
def _fresh_registry():
    _reset_registry()
    yield
    ToolRegistry._tools.clear()
    ToolRegistry._activated_risk.clear()


def _tool_logs(**over):
    return ToolRegistry.get("query_logs.v1")


def _metrics_tool(**over):
    return ToolRegistry.get("query_metrics.v1")


TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
REQ_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
QUERY_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
TIME_RANGE = "2026-08-20T00:00:00Z/2026-08-20T01:00:00Z"
SRC = "query-api"


def _now():
    return datetime.now(timezone.utc)


def _base_kwargs(tool=None, **over):
    kw = dict(
        tool=tool or _tool_logs(),
        tenant_id=TENANT,
        cluster_id=CLUSTER,
        request_id=REQ_ID,
        query_id=QUERY_ID,
        time_range=TIME_RANGE,
        source_system=SRC,
        started_at=_now(),
        finished_at=_now(),
    )
    kw.update(over)
    return kw


# ═══════════════════════════════════════════════════════
#  T1 状态归一化
# ═══════════════════════════════════════════════════════

class TestT1Normalization:
    def test_success_with_data(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}], "total": 1})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "success"
        assert res.data == {"logs": [{"ts": 1}], "total": 1}
        assert res.error_code == ""

    def test_empty_result_no_data(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [], "total": 0})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "no_data"

    def test_no_data_error_body(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"error": "NO_DATA", "message": "no data"})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "no_data"

    def test_unavailable(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="unavailable", http_status=503, message="vm down")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "unavailable"
        assert res.retryable is True

    def test_permission_denied(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="unauthorized capability")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "permission_denied"
        assert res.denied_scope is not None
        assert res.denied_scope["required_capability"] == "observability.logs.read"

    def test_timeout(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="timeout", http_status=504, message="deadline exceeded")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "timeout"
        assert res.retryable is True

    def test_failed(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="validation_failed", http_status=422, message="bad request")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "failed"

    def test_partial(self):
        from tool_result import combine_partial

        r1 = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        r2 = InternalQueryError(kind="timeout", http_status=504, message="slow")
        res = combine_partial([r1, r2], partial_reason="timeout_partial", **_base_kwargs())
        assert res.status == "partial"
        assert res.partial_reason == "timeout_partial"


# ═══════════════════════════════════════════════════════
#  T2 禁止降级（§94 红线）
# ═══════════════════════════════════════════════════════

class TestT2NoDowngrade:
    def test_permission_denied_not_no_data(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="denied")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "permission_denied"  # 不降级为 no_data

    def test_no_data_not_healthy(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"error": "NO_DATA"})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "no_data"  # 不降级为 success/healthy

    def test_unavailable_not_healthy(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="unavailable", http_status=503, message="down")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "unavailable"  # 不降级为 success/healthy

    def test_backend_403_not_no_data(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="tenant denied")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "permission_denied"  # 403 绝不→no_data

    def test_network_error_not_no_data(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="service_auth_failed", http_status=401, message="no route")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.status == "failed"  # 网络/服务错误→failed，非 no_data


# ═══════════════════════════════════════════════════════
#  T3 重试与超时
# ═══════════════════════════════════════════════════════

class TestT3RetryTimeout:
    def test_permission_denied_not_retryable(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="denied")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.retryable is False

    def test_no_data_not_retryable(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"error": "NO_DATA"})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.retryable is False

    def test_failed_retryable(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="validation_failed", http_status=422, message="bad")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.retryable is True

    def test_retry_upper_bound(self):
        from tool_result import normalize_tool_result

        # 重试受 tool.retry 上限约束：retry_policy.max_attempts 不无限
        outcome = InternalQueryError(kind="unavailable", http_status=503, message="down")
        res = normalize_tool_result(
            outcome=outcome,
            **_base_kwargs(retry_policy={"max_attempts": 2, "backoff": 1.0}),
        )
        assert res.retry_policy["max_attempts"] == 2
        assert res.status == "unavailable"  # 到达上限后仍为 unavailable，不伪装成功


# ═══════════════════════════════════════════════════════
#  T4 Evidence 兼容
# ═══════════════════════════════════════════════════════

class TestT4EvidenceCompat:
    def test_evidence_ids_populated(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs(evidence_ids=["ev-1", "ev-2"]))
        assert res.evidence_ids == ["ev-1", "ev-2"]

    def test_evidence_required_from_tool(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        # query_logs.v1 evidence_required=True（P7.1）
        assert res.evidence_required is True

    def test_source_system_no_ai(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        # 审计 P0-3：非法 source（LLM）必须拒绝，不得伪装成 query-api
        with pytest.raises(ValueError):
            normalize_tool_result(outcome=outcome, **_base_kwargs(source_system="LLM"))


# ═══════════════════════════════════════════════════════
#  T5 Schema 完整
# ═══════════════════════════════════════════════════════

class TestT5Schema:
    def test_invalid_status_rejected(self):
        from tool_result import ToolResult

        with pytest.raises(ValueError):
            ToolResult(
                tool_name="query_logs", tool_id="query_logs.v1", cluster_id=CLUSTER, tenant_id=TENANT,
                status="bogus", summary="", data={}, error_code="", error_message="",
                retryable=False, retry_policy={}, evidence_ids=[], evidence_required=True,
                source_system=SRC, request_id=REQ_ID, query_id=QUERY_ID, time_range=TIME_RANGE,
                started_at=_now(), finished_at=_now(), duration_ms=1, provenance={},
            )

    def test_missing_required_field_rejected(self):
        from tool_result import validate_tool_result

        from tool_result import ToolResult

        tr = ToolResult(
            tool_name="query_logs", tool_id="query_logs.v1", cluster_id=CLUSTER, tenant_id=TENANT,
            status="success", summary="", data={}, error_code="", error_message="",
            retryable=False, retry_policy={}, evidence_ids=[], evidence_required=True,
            source_system="", request_id=REQ_ID, query_id=QUERY_ID, time_range="",
            started_at=_now(), finished_at=_now(), duration_ms=1, provenance={},
        )
        assert validate_tool_result(tr) is not None  # 缺 source_system/time_range → 校验失败


# ═══════════════════════════════════════════════════════
#  T6 Context Traceability
# ═══════════════════════════════════════════════════════

class TestT6Traceability:
    def test_request_id_flow(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.request_id == REQ_ID
        assert res.provenance["request_id"] == REQ_ID
        assert res.provenance["tool_id"] == "query_logs.v1"

    def test_permission_denied_denied_scope(self):
        from tool_result import normalize_tool_result

        outcome = InternalQueryError(kind="permission_denied", http_status=403, message="denied")
        res = normalize_tool_result(outcome=outcome, **_base_kwargs())
        assert res.denied_scope["required_capability"] == "observability.logs.read"
        assert res.denied_scope["denied_scope"] is not None

    def test_partial_reason_present(self):
        from tool_result import combine_partial

        r1 = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        r2 = InternalQueryError(kind="timeout", http_status=504, message="slow")
        res = combine_partial([r1, r2], partial_reason="source_partial", **_base_kwargs())
        assert res.partial_reason == "source_partial"

    def test_source_system_no_ai(self):
        from tool_result import normalize_tool_result

        outcome = QueryResult(http_status=200, body={"logs": [{"ts": 1}]})
        # 审计 P0-3：非法 source（Agent）必须拒绝，不得伪装成 query-api
        with pytest.raises(ValueError):
            normalize_tool_result(outcome=outcome, **_base_kwargs(source_system="Agent"))
