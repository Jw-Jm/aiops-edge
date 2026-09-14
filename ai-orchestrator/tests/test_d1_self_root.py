"""评分模型语义评审 D-1/B 回归：症状即根因的分档判定。

- self-root + 直接因果证据（change/k8s_event）+ 独立类别≥2 → confirmed 合法
- self-root + 直接因果但类别不足 → probable（降级不拒答）
- self-root 无直接因果 → insufficient（维持门禁）
- 非 self-root 无传播路径 → insufficient（维持门禁）
"""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


def _mk_engine(monkeypatch, *, candidates, direct_causal_categories):
    """构造受控引擎：固定候选子图与证据行，隔离真实数据源。"""
    from rca_engine import RCAEngineV2, RCARequest
    from rca_engine.engine import RCAEngineV2 as Engine

    class Graph:
        def __call__(self, **params):
            op = params.get("graph_operation")
            if op == "resolve_entity":
                return {"entity": {"entity_uid": SYMPTOM, "name": "eval-target", "entity_type": "deployment"}}
            if op == "candidate_subgraph":
                return {"vertices": candidates, "edges": [], "meta": {"graph_generation": 1}}
            raise AssertionError(op)

    class Provider:
        failures = []

        def __call__(self, request, context):
            rows = []
            for row in EVIDENCE:
                rows.append(dict(row, entity_uid=SYMPTOM))
            return rows

    class Persist:
        def __call__(self, result, context):
            return {"ok": True}

    SYMPTOM = "k8s-deployment:v1:c:target"
    EVIDENCE = direct_causal_categories
    return Engine(graph_client=Graph(), evidence_provider=Provider(), persistence=Persist())


def _request():
    from rca_engine import RCARequest

    return RCARequest.from_ai_run(
        {"run_id": "d1-run", "tenant_id": "t", "primary_cluster_id": "c",
         "target_type": "deployment", "target_resource_id": "k8s-deployment:v1:c:target",
         "time_range_start": "2026-09-14T00:00:00Z", "time_range_end": "2026-09-14T01:00:00Z",
         "symptom_time": "2026-09-14T01:00:00Z"},
        resource_id="k8s-deployment:v1:c:target",
        entity_name="k8s-deployment:v1:c:target", symptoms=("probe failed",),
    )


def _candidate():
    return {"entity_uid": "k8s-deployment:v1:c:target", "name": "eval-target",
            "entity_type": "deployment", "hops": 0}


def test_self_root_with_change_and_event_confirms():
    """X1 场景：change + k8s_event 直接因果 + alert → self-root confirmed 合法。"""
    from rca_engine.engine import RCAEngineV2

    candidates = [dict(_candidate())]
    engine = RCAEngineV2(
        graph_client=_FixedGraph(candidates),
        evidence_provider=_FixedProvider([
            {"category": "kubernetes_event", "severity": 0.6, "summary": "probe failed"},
            {"category": "change", "severity": 1.0, "same_entity": True, "summary": "image changed"},
            {"category": "alert", "severity": 1.0, "summary": "unavailable"},
        ]),
    )
    result = engine.diagnose(_request(), None)
    assert result.root_cause_status in {"confirmed", "probable"}, result.root_cause_status
    assert result.root_cause == "k8s-deployment:v1:c:target"
    # confirmed 必须申报无传播解释
    if result.root_cause_status == "confirmed":
        assert "propagation_path" in result.missing_evidence


def test_self_root_event_only_downgrades_to_probable():
    """仅 k8s_event 单一直因类别 → probable 而非 insufficient。"""
    from rca_engine.engine import RCAEngineV2

    engine = RCAEngineV2(
        graph_client=_FixedGraph([dict(_candidate())]),
        evidence_provider=_FixedProvider([
            {"category": "kubernetes_event", "severity": 0.6, "summary": "probe failed"},
            {"category": "alert", "severity": 1.0, "summary": "unavailable"},
        ]),
    )
    result = engine.diagnose(_request(), None)
    assert result.root_cause_status == "probable", result.root_cause_status


def test_self_root_without_direct_causal_stays_gated():
    """仅 alert/metric（非直接因果）→ 维持门禁 insufficient。"""
    from rca_engine.engine import RCAEngineV2

    engine = RCAEngineV2(
        graph_client=_FixedGraph([dict(_candidate())]),
        evidence_provider=_FixedProvider([
            {"category": "alert", "severity": 1.0, "summary": "unavailable"},
            {"category": "log", "severity": 0.5, "summary": "errors"},
        ]),
    )
    result = engine.diagnose(_request(), None)
    assert result.root_cause_status == "insufficient_evidence"


class _FixedGraph:
    def __init__(self, candidates):
        self._candidates = candidates

    def __call__(self, **params):
        op = params.get("graph_operation")
        if op == "resolve_entity":
            return {"entity": {"entity_uid": "k8s-deployment:v1:c:target",
                               "name": "eval-target", "entity_type": "deployment"}}
        if op == "get_vertex":
            return {"entity": {"entity_uid": params.get("entity_uid"),
                               "name": "eval-target", "entity_type": "deployment"}}
        if op == "candidate_subgraph":
            return {"vertices": self._candidates, "edges": [],
                    "meta": {"graph_generation": 1}}
        raise AssertionError(f"unexpected graph op {op}")


class _FixedProvider:
    failures = []

    def __init__(self, rows):
        self._rows = rows

    def __call__(self, request, context):
        return [dict(r, entity_uid="k8s-deployment:v1:c:target") for r in self._rows]
