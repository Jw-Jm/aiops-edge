"""D16b 回归：实体未建模 ≠ 图谱不可用。

真实环境验证发现：目标对象未建模进图谱（如业务服务 regression-svc）时，
resolve_entity 返回 not_found，引擎把 GRAPH_ENTITY_NOT_FOUND 与图谱系统故障
混在同一 fail-closed 分支 → 跳过 metrics/logs/alerts/events 证据采集 →
所有调查 0 证据。修复后：实体未解析仍执行本地证据采集，仅缺 graph_relation。
"""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from rca_engine.engine import RCAEngineV2, RCARequest


class _Request:
    run_id = "d16b-run"
    tenant_id = "tenant-1"
    cluster_id = "cluster-1"
    resource_id = "unmodeled-svc"
    entity_uid = ""
    entity_name = "unmodeled-svc"
    target_type = "service"
    window_start = "2026-09-14T00:00:00Z"
    window_end = "2026-09-14T01:00:00Z"
    symptom_time = "2026-09-14T01:00:00Z"
    evidence = ()


class _GraphDown:
    """resolve_entity 系统故障（如连接失败）→ fail-closed local-only。"""

    def __call__(self, **params):
        raise RuntimeError("hugegraph connection refused")


class _EntityNotFound:
    def __call__(self, **params):
        op = params.get("graph_operation")
        if op == "resolve_entity":
            raise LookupError("GRAPH_ENTITY_NOT_FOUND")
        raise AssertionError("engine must not issue graph reads for an unresolved entity")


class _EvidenceProvider:
    """本地证据采集（metrics/logs/alerts/events，不依赖图谱）。"""

    def __init__(self):
        self.failures: list[str] = []

    def __call__(self, request, context):
        return [
            {"evidence_id": "ev-1", "evidence_type": "logs", "summary": "5xx spike"},
            {"evidence_id": "ev-2", "evidence_type": "alerts", "summary": "critical firing"},
        ]


class _Persistence:
    def __init__(self):
        self.calls: list[tuple] = []

    def __call__(self, result, context):
        self.calls.append((result, context))
        return {"ok": True}


def _request():
    r = _Request()
    return RCARequest.from_ai_run(
        {"run_id": r.run_id, "tenant_id": r.tenant_id, "primary_cluster_id": r.cluster_id,
         "target_type": r.target_type, "target_resource_id": r.resource_id,
         "time_range_start": r.window_start, "time_range_end": r.window_end,
         "symptom_time": r.symptom_time},
        resource_id=r.resource_id, entity_name=r.entity_name, symptoms=("err",),
    )


def test_entity_unresolved_still_collects_local_evidence():
    persistence = _Persistence()
    provider = _EvidenceProvider()
    result = RCAEngineV2(
        graph_client=_EntityNotFound(), evidence_provider=provider, persistence=persistence,
    ).diagnose(_request(), None)
    # 关键断言：本地证据必须被采集（此前为 0）
    assert len(result.evidence) >= 2, "unresolved entity must not skip local evidence collection"
    assert result.root_cause_status == "insufficient_evidence"
    assert "graph_relation" in result.missing_evidence
    # 语义区分：实体未解析不是图谱故障
    ctx = result.graph_context.get("context") or result.graph_context
    warnings = ctx.get("warning_codes") or []
    assert "GRAPH_ENTITY_UNRESOLVED" in warnings
    assert "GRAPH_UNAVAILABLE" not in warnings
    assert result.graph_context.get("partial") is True or ctx.get("partial") is True
    assert persistence.calls, "unresolved-entity result must remain replayable"


def test_graph_system_failure_stays_fail_closed():
    result = RCAEngineV2(graph_client=_GraphDown(), evidence_provider=_EvidenceProvider()).diagnose(
        _request(), None,
    )
    # 系统故障路径保持原 fail-closed 语义（不采集数据面证据）
    ctx = result.graph_context.get("context") or result.graph_context
    assert "GRAPH_UNAVAILABLE" in (ctx.get("warning_codes") or [])
    assert result.root_cause_status == "insufficient_evidence"
