import pytest
from types import SimpleNamespace

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


def test_evidence_query_failed_envelope_is_not_treated_as_empty_success():
    class Response:
        body = {
            "quality": "failed",
            "source_errors": ["CLICKHOUSE password=super-secret host=10.0.0.7"],
        }

    class Client:
        def query(self, **_kwargs):
            return Response()

    provider = InvestigationEvidenceProvider.__new__(InvestigationEvidenceProvider)
    provider.item = SimpleNamespace(
        request_context=SimpleNamespace(executor_id="worker-1", lease_epoch=1, lease_token="lease-token"),
        run_id="11111111-1111-4111-8111-111111111111", invocation_id="22222222-2222-4222-8222-222222222222",
        tenant_id="33333333-3333-4333-8333-333333333333", cluster_id="44444444-4444-4444-8444-444444444444",
        request_id="55555555-5555-4555-8555-555555555555",
        window_start="2026-09-03T00:00:00Z", window_end="2026-09-03T01:00:00Z",
    )
    provider.client = Client()
    provider.failures = []
    with pytest.raises(RuntimeError, match="QUERY_FAILED"):
        provider._query("query_logs.v1", "logs", {"services": ["api"]})


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


def test_ipmi_error_type_is_normalized_to_hardware_severity_when_missing():
    provider = InvestigationEvidenceProvider.__new__(InvestigationEvidenceProvider)
    output = []
    provider._append(output, "kubernetes_event", {"events": [
        {"source": "ipmi-sel", "node": "node-a", "type": "Error",
         "event_time": "2026-09-02T00:00:00Z"},
    ]}, [{"entity_uid": "node:node-a", "name": "node-a"}])
    assert output[0]["category"] == "hardware_sel"
    assert output[0]["severity"] == 1.0


def test_trace_error_count_is_normalized_to_degraded_evidence():
    provider = InvestigationEvidenceProvider.__new__(InvestigationEvidenceProvider)
    output = []
    provider._append(output, "trace", {"traces": [
        {"TraceID": "trace-1", "ErrorCount": 2, "ServiceNames": ["api"],
         "Start": "2026-09-02T00:00:00Z"},
    ]}, [{"entity_uid": "service:api", "name": "api"}])
    assert len(output) == 1
    assert output[0]["error_count"] == 2
    assert output[0]["degraded"] is True
