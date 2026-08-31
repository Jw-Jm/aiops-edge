import pytest

from rca_engine.runtime import InvestigationEvidenceProvider, _unwrap_query_payload


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
