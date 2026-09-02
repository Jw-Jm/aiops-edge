import pytest

from rca_engine.runtime import (
    InvestigationEvidenceProvider,
    _candidate_uids_for_row,
    _unwrap_query_payload,
)


def test_graph_adapter_payload_unwraps_tool_result_envelope():
    assert _unwrap_query_payload({"quality": "complete", "data": {"entity": {"entity_uid": "u1"}}}) == {
        "entity": {"entity_uid": "u1"}
    }


def test_graph_adapter_does_not_turn_failed_query_into_empty_graph():
    with pytest.raises(RuntimeError, match="GRAPH_UNAVAILABLE"):
        _unwrap_query_payload({"quality": "failed", "source_errors": ["GRAPH_UNAVAILABLE"]})


def test_evidence_provider_exposes_failure_accumulator(monkeypatch):
    monkeypatch.setattr("rca_engine.runtime._client", lambda: object())
    provider = InvestigationEvidenceProvider(object())
    assert provider.failures == []


def test_evidence_row_service_names_bind_to_stable_graph_entities():
    candidates = [
        {"entity_uid": "uid-orch", "name": "ai-orchestrator"},
        {"entity_uid": "uid-vm", "name": "victoria-metrics"},
    ]
    assert _candidate_uids_for_row({"ServiceNames": ["ai-orchestrator", "victoria-metrics"]}, candidates) == [
        "uid-orch", "uid-vm"
    ]


def test_evidence_row_kubernetes_pod_name_binds_only_matching_candidate():
    candidates = [{"entity_uid": "uid-orch", "name": "ai-orchestrator"}, {"entity_uid": "uid-api", "name": "api"}]
    assert _candidate_uids_for_row({"ServiceName": "observability/ai-orchestrator-56ddf4c54c-t5xm2"}, candidates) == ["uid-orch"]


def test_evidence_row_without_identity_remains_unbound_when_batch_scoped():
    candidates = [{"entity_uid": "uid-orch", "name": "ai-orchestrator"}, {"entity_uid": "uid-vm", "name": "victoria-metrics"}]
    assert _candidate_uids_for_row({"TraceID": "trace-1"}, candidates) == []
