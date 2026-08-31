from __future__ import annotations

import pytest

from rca_engine import RCARequest
from rca_engine.engine import RCAEngineV2
from rca_engine.evidence import evidence_for_candidate, independent_categories
from rca_engine.scorer import deterministic_temporal_score


def _entity(uid: str, name: str) -> dict:
    return {"entity_uid": uid, "entity_type": "service", "name": name}


def test_temporal_score_uses_frozen_symptom_time_and_fixed_bands():
    symptom = "2026-08-27T00:30:00Z"
    assert deterministic_temporal_score([{"observed_at": "2026-08-27T00:25:00Z"}], symptom) == 1.0
    assert deterministic_temporal_score([{"observed_at": "2026-08-27T00:15:00Z"}], symptom) == 0.8
    assert deterministic_temporal_score([{"observed_at": "2026-08-26T23:30:00Z"}], symptom) == 0.5
    assert deterministic_temporal_score([{"observed_at": "2026-08-27T00:31:00Z"}], symptom) == 0.4
    assert deterministic_temporal_score([{"observed_at": "2026-08-27T00:33:00Z", "temporal_score": 1.0}], symptom) == 0.0
    assert deterministic_temporal_score([{"observed_at": "2026-08-26T23:59:00Z"}], symptom,
                                        window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z") == 0.0
    assert deterministic_temporal_score([{"t": 1787790300000}], symptom) == 1.0
    assert deterministic_temporal_score([{"t": "1787790300000"}], symptom) == 1.0


def test_request_factory_copies_ai_run_time_range_without_wall_clock_defaults():
    request = RCARequest.from_ai_run({"run_id": "run-1", "tenant_id": "tenant-1", "primary_cluster_id": "cluster-1",
                                      "target_resource_id": "service:checkout", "time_range_start": "2026-08-27T00:00:00Z",
                                      "time_range_end": "2026-08-27T01:00:00Z"})
    assert request.window_start == "2026-08-27T00:00:00Z"
    assert request.window_end == "2026-08-27T01:00:00Z"
    assert request.symptom_time == request.window_end
    assert request.cluster_ids == ("cluster-1",)
    assert request.target_type == "service"
    assert request.target_resource_id == "service:checkout"
    fallback = RCARequest.from_ai_run({"run_id": "run-1", "tenant_id": "tenant-1", "cluster_id": "cluster-1",
                                       "time_range_start": "2026-08-27T00:00:00Z", "time_range_end": "2026-08-27T01:00:00Z"})
    assert fallback.cluster_id == "cluster-1"
    with pytest.raises(ValueError, match="within"):
        RCARequest(run_id="run-1", tenant_id="tenant-1", cluster_id="cluster-1",
                   window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
                   symptom_time="2026-08-27T01:01:00Z")
    with pytest.raises(ValueError, match="immutable"):
        RCARequest.from_ai_run({"run_id": "run-1", "tenant_id": "tenant-1", "primary_cluster_id": "cluster-1",
                                "time_range_start": "2026-08-27T00:00:00Z", "time_range_end": "2026-08-27T01:00:00Z"},
                               window_start="2026-08-26T00:00:00Z")
    with pytest.raises(ValueError, match="immutable"):
        RCARequest.from_ai_run({"run_id": "run-1", "tenant_id": "tenant-1", "primary_cluster_id": "cluster-1",
                                "time_range_start": "2026-08-27T00:00:00Z", "time_range_end": "2026-08-27T01:00:00Z"},
                               tenant_id="other-tenant")


def test_request_accepts_documented_canonical_constructor_fields():
    request = RCARequest(
        run_id="run-canonical", tenant_id="tenant-1", cluster_ids=("cluster-1",),
        target_type="pod", target_resource_id="pod:checkout",
        window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
        symptom_time="2026-08-27T00:30:00Z",
    )
    assert request.cluster_id == "cluster-1"
    assert request.target_type == "pod"
    assert request.resource_id == "pod:checkout"


def test_rca_request_and_result_keep_frozen_window_and_actual_paths():
    graph = {
        "vertices": [_entity("service:db", "db"), _entity("service:checkout", "checkout"), _entity("service:noise", "noise")],
        "edges": [
            {"edge_uid": "edge:checkout-db", "source_uid": "service:checkout", "target_uid": "service:db", "propagates_failure": True, "confidence": 1.0, "candidate_direction": "OUT"},
            {"edge_uid": "edge:checkout-noise", "source_uid": "service:checkout", "target_uid": "service:noise", "propagates_failure": True, "confidence": 0.7, "candidate_direction": "OUT"},
            {"edge_uid": "edge:noise-db", "source_uid": "service:noise", "target_uid": "service:db", "propagates_failure": False, "candidate_direction": "OUT"},
        ],
    }
    seen = {}

    def graph_client(**params):
        if params["graph_operation"] == "get_vertex":
            return {"entity": _entity("service:checkout", "checkout")}
        if params["graph_operation"] == "candidate_subgraph":
            return graph
        raise AssertionError(params)

    def evidence_provider(request, _context):
        seen["request"] = request
        return [
            {"entity_uid": "service:db", "category": "metric", "severity": 1.0, "observed_at": "2026-08-27T00:25:00Z"},
            {"entity_uid": "service:db", "category": "alert", "severity": 1.0, "observed_at": "2026-08-27T00:25:00Z"},
        ]

    request = RCARequest(run_id="run-1", tenant_id="tenant-1", cluster_id="cluster-1",
                         window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
                         symptom_time="2026-08-27T00:30:00Z", entity_uid="service:checkout")
    result = RCAEngineV2(graph_client=graph_client, evidence_provider=evidence_provider).diagnose(request)

    assert seen["request"].window_start == "2026-08-27T00:00:00Z"
    assert result.window_end == "2026-08-27T01:00:00Z"
    assert result.graph_context["symptom_time"] == "2026-08-27T00:30:00Z"
    assert result.root_cause == "service:db"
    assert len(result.propagation_paths) == 1
    path = result.propagation_paths[0]
    assert path["vertex_uids"] == ["service:db", "service:checkout"]
    assert path["edge_uids"] == ["edge:checkout-db"]


def test_graph_outage_persists_local_only_context_for_replay():
    persisted = []

    def graph_client(**_params):
        raise RuntimeError("query-api unavailable")

    request = RCARequest(
        run_id="run-graph-down", tenant_id="tenant-1", cluster_id="cluster-1",
        window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
        symptom_time="2026-08-27T00:30:00Z", entity_uid="service:checkout",
    )
    result = RCAEngineV2(
        graph_client=graph_client,
        persistence=lambda result_payload, context_payload: persisted.append(
            (result_payload, context_payload)
        ),
    ).diagnose(request)

    assert result.root_cause_scope == "local_only"
    assert result.graph_enhanced is False
    assert len(persisted) == 1
    assert persisted[0][1]["warning_codes"][0] == "GRAPH_UNAVAILABLE"


def test_unbound_evidence_is_context_only_and_never_scores_candidate():
    items = [
        {"category": "metric", "severity": 1.0, "entity_uid": "service:other"},
        {"category": "trace", "severity": 1.0},
        {"category": "alert", "severity": 1.0, "entity_uid": "service:db"},
    ]
    scoped = evidence_for_candidate(items, "service:db")
    assert [item["category"] for item in scoped] == ["alert"]


def test_provenance_groups_not_categories_define_independence():
    assert independent_categories([
        {"category": "metric", "correlation_group": "query-1"},
        {"category": "alert", "correlation_group": "query-1"},
    ]) == 1
    assert independent_categories([
        {"category": "metric", "correlation_group": "query-1"},
        {"category": "trace", "correlation_group": "query-2"},
    ]) == 2


def test_contradiction_downgrades_candidate_to_insufficient_evidence():
    graph = {
        "vertices": [_entity("service:db", "db"), _entity("service:checkout", "checkout")],
        "edges": [{"edge_uid": "edge:checkout-db", "source_uid": "service:checkout",
                   "target_uid": "service:db", "propagates_failure": True,
                   "confidence": 1.0, "candidate_direction": "OUT"}],
    }

    def graph_client(**params):
        if params["graph_operation"] == "get_vertex":
            return {"entity": _entity("service:checkout", "checkout")}
        return graph

    request = RCARequest(run_id="run-contradiction", tenant_id="tenant-1", cluster_id="cluster-1",
                         window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
                         symptom_time="2026-08-27T00:30:00Z", entity_uid="service:checkout",
                         evidence=(
                             {"entity_uid": "service:db", "category": "metric", "severity": 1.0,
                              "observed_at": "2026-08-27T00:25:00Z", "correlation_group": "metric-1"},
                             {"entity_uid": "service:db", "category": "trace", "degraded": True,
                              "observed_at": "2026-08-27T00:25:00Z", "correlation_group": "trace-1",
                              "contradicts": True, "contradiction_code": "TRACE_HEALTHY"},
                         ))
    result = RCAEngineV2(graph_client=graph_client).diagnose(request)
    assert result.root_cause_status == "insufficient_evidence"
    assert result.contradictions[0]["code"] == "TRACE_HEALTHY"
