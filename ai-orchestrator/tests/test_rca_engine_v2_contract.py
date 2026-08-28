from __future__ import annotations

import pytest

from rca_engine import RCARequest
from rca_engine.engine import RCAEngineV2
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


def test_request_factory_copies_ai_run_time_range_without_wall_clock_defaults():
    request = RCARequest.from_ai_run({"run_id": "run-1", "tenant_id": "tenant-1", "primary_cluster_id": "cluster-1",
                                      "target_resource_id": "service:checkout", "time_range_start": "2026-08-27T00:00:00Z",
                                      "time_range_end": "2026-08-27T01:00:00Z"})
    assert request.window_start == "2026-08-27T00:00:00Z"
    assert request.window_end == "2026-08-27T01:00:00Z"
    assert request.symptom_time == request.window_end
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


def test_rca_request_and_result_keep_frozen_window_and_actual_paths():
    graph = {
        "vertices": [_entity("service:db", "db"), _entity("service:checkout", "checkout"), _entity("service:noise", "noise")],
        "edges": [
            {"edge_uid": "edge:db-checkout", "source_uid": "service:db", "target_uid": "service:checkout", "propagates_failure": True, "candidate_direction": "OUT"},
            {"edge_uid": "edge:checkout-noise", "source_uid": "service:checkout", "target_uid": "service:noise", "propagates_failure": True, "candidate_direction": "OUT"},
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
    assert path["edge_uids"] == ["edge:db-checkout"]


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
