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


def test_evidence_row_kubernetes_event_target_binds_to_stable_candidate():
    candidates = [{"entity_uid": "uid-orch", "name": "ai-orchestrator"}, {"entity_uid": "uid-api", "name": "api"}]
    assert _candidate_uids_for_row({"involved_object": "Deployment/ai-orchestrator"}, candidates) == ["uid-orch"]


def test_evidence_row_without_identity_remains_unbound_when_batch_scoped():
    candidates = [{"entity_uid": "uid-orch", "name": "ai-orchestrator"}, {"entity_uid": "uid-vm", "name": "victoria-metrics"}]
    assert _candidate_uids_for_row({"TraceID": "trace-1"}, candidates) == []


def test_ipmi_event_source_is_hardware_evidence_only_when_explicit():
    provider = InvestigationEvidenceProvider.__new__(InvestigationEvidenceProvider)
    output = []
    provider._append(output, "kubernetes_event", {"events": [
        {"source": "ipmi-sel", "node": "node-a", "type": "Error", "severity": "critical",
         "event_time": "2026-09-02T00:00:00Z"},
        {"source": "k8s", "involved_object": "Pod/api", "type": "Warning",
         "event_time": "2026-09-02T00:00:00Z"},
    ]}, [{"entity_uid": "node:node-a", "name": "node-a"},
        {"entity_uid": "service:api", "name": "api"}])
    assert [item["category"] for item in output] == ["hardware_sel", "kubernetes_event"]
    assert output[0]["severity"] == 1.0
