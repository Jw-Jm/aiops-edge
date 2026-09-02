import pytest
from types import SimpleNamespace

from investigation_dispatcher import AcceptedInvocation
from invocation_scope import current_execution_lease_token
from investigation_runtime import (
    InvestigationRuntime,
    _normalize_investigation_outcome,
    _runtime_events,
)


class Lease:
    def __init__(self):
        self.checked = 0
        self.closed = 0
        self._token = "lease-token"
        self.commits = []

    def check_active(self):
        self.checked += 1

    def commit(self, **kwargs):
        self.commits.append(kwargs)
        return {"status": kwargs["target"]}

    def close(self):
        self.closed += 1


class LeaseExecutor:
    def __init__(self):
        self.calls = []
        self.lease = Lease()

    def acquire(self, **kwargs):
        self.calls.append(kwargs)
        return self.lease


class ControlPlane:
    def __init__(self):
        self.status = "created"
        self.version = 0
        self.transitions = []

    def get(self, **kwargs):
        return {"run": {"status": self.status, "state_version": self.version}}

    def transition(self, *, run_id, target, expected_version, tenant_id, command_id):
        assert expected_version == self.version
        self.transitions.append((target, expected_version))
        self.status = target
        self.version += 1
        return {"run": {"status": target, "state_version": self.version}}


class Brain:
    async def investigate(self, item, lease):
        lease.check_active()
        return [{"type": "evidence", "id": "ev-1"}]


class LeaseAwareBrain:
    def __init__(self):
        self.token = ""

    async def investigate(self, item, lease):
        self.token = current_execution_lease_token()
        return [{"type": "evidence", "id": "ev-1"}]


class OutcomeBrain:
    def __init__(self, outcome):
        self.outcome = outcome

    async def investigate(self, item, lease):
        return self.outcome


@pytest.mark.asyncio
async def test_worker_brain_keeps_rca_payload_for_terminal_context(monkeypatch):
    from apps.investigation import _WorkerBrain

    class FakeRCAResult:
        root_cause_status = "confirmed"
        root_cause = "service:db"
        confidence = 0.9
        propagation_paths = [{"vertex_uids": ["service:db", "service:api"]}]
        window_start = "2026-08-27T00:00:00Z"
        window_end = "2026-08-27T01:00:00Z"
        symptom_time = "2026-08-27T00:30:00Z"
        graph_enhanced = True
        graph_context = {"warning_codes": [], "partial": False, "stale": False}
        explanation = "confirmed"

        def to_dict(self):
            return {"root_cause_status": self.root_cause_status,
                    "root_cause": self.root_cause,
                    "confidence": self.confidence,
                    "propagation_paths": self.propagation_paths,
                    "evidence": [{"category": "metric", "entity_uid": "service:db"},
                                 {"category": "trace", "entity_uid": "service:db"}],
                    "final_graph_context": {"final": True, "vertices": [{"entity_uid": "service:db"}]},
                    "propagation_path": [{"entity_uid": "service:db"}, {"entity_uid": "service:api"}],
                    "subgraph_node_count": 3,
                    "root_score": 0.9,
                    "deterministic_root_score": 0.9,
                    "graph_context": self.graph_context}

    class FakeEngine:
        def __init__(self, **_kwargs):
            pass

        def diagnose(self, *_args):
            return FakeRCAResult()

    monkeypatch.setattr("rca_engine.engine.RCAEngineV2", FakeEngine)
    monkeypatch.setattr("rca_engine.runtime.InvestigationGraphClient", lambda _item: object())
    monkeypatch.setattr("rca_engine.runtime.InvestigationEvidenceProvider", lambda _item: object())
    item = AcceptedInvocation(
        run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999",
        request_id="11111111-1111-4111-8111-111111111111",
        tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666",
        intent="diagnose", resource_id="service:api", service="service:api",
        message="diagnose", action_mode="read_only", request_context=SimpleNamespace(),
        window_start="2026-08-27T00:00:00Z", window_end="2026-08-27T01:00:00Z",
        symptom_time="2026-08-27T00:30:00Z",
    )
    result = await _WorkerBrain().investigate(item, Lease())

    assert result["result"]["rca"]["root_cause_status"] == "confirmed"
    rca_event = next(event for event in result["events"] if event.get("event_type") == "rca.v2")
    assert len(rca_event["evidence"]) == 2
    assert rca_event["final_graph_context"]["final"] is True
    assert rca_event["root_score"] == rca_event["deterministic_root_score"]
    assert rca_event["propagation_path"][0]["entity_uid"] == "service:db"


def item():
    return AcceptedInvocation(
        run_id="22222222-2222-4222-8222-222222222222",
        invocation_id="99999999-9999-4999-8999-999999999999",
        request_id="11111111-1111-4111-8111-111111111111",
        tenant_id="55555555-5555-4555-8555-555555555555",
        cluster_id="66666666-6666-4666-8666-666666666666",
        intent="diagnose", resource_id="service-a", service="service-a",
        message="diagnose", action_mode="read_only",
    )


@pytest.mark.asyncio
async def test_accept_claims_lease_and_advances_to_planning():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(control_plane=cp, lease_executor=leases, brain=Brain())
    work = await runtime.accept(item())
    assert cp.status == "planning"
    assert cp.transitions == [("planning", 0)]
    assert leases.calls[0]["run_id"] == item().run_id
    assert work.lease is leases.lease


@pytest.mark.asyncio
async def test_execute_progresses_investigating_verifying_and_success():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(control_plane=cp, lease_executor=leases, brain=Brain())
    work = await runtime.accept(item())
    await runtime.execute(work)
    assert [name for name, _ in cp.transitions] == ["planning", "investigating", "verifying"]
    assert leases.lease.checked >= 1
    assert leases.lease.closed == 1


@pytest.mark.asyncio
async def test_execute_binds_ephemeral_lease_token_for_tools():
    cp = ControlPlane()
    leases = LeaseExecutor()
    brain = LeaseAwareBrain()
    runtime = InvestigationRuntime(control_plane=cp, lease_executor=leases, brain=brain)
    work = await runtime.accept(item())
    await runtime.execute(work)
    assert brain.token == "lease-token"


@pytest.mark.asyncio
async def test_execute_commits_failed_outcome_instead_of_false_success():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(
        control_plane=cp, lease_executor=leases,
        brain=OutcomeBrain({"status": "failed", "events": [], "error_code": "QUERY_FAILED"}),
    )
    work = await runtime.accept(item())
    await runtime.execute(work)
    assert leases.lease.commits[-1]["target"] == "failed"
    assert leases.lease.commits[-1]["result"]["error_code"] == "QUERY_FAILED"


@pytest.mark.asyncio
async def test_execute_preserves_partial_outcome():
    cp = ControlPlane()
    leases = LeaseExecutor()
    runtime = InvestigationRuntime(
        control_plane=cp, lease_executor=leases,
        brain=OutcomeBrain({"status": "partial", "events": [{"type": "evidence"}]}),
    )
    work = await runtime.accept(item())
    await runtime.execute(work)
    assert leases.lease.commits[-1]["target"] == "partial"


def test_runtime_event_ids_are_stable_for_retries():
    first = _runtime_events([{"type": "progress", "node": "collect"}],
                            invocation_id=item().invocation_id, target="success", result={})
    second = _runtime_events([{"type": "progress", "node": "collect"}],
                             invocation_id=item().invocation_id, target="success", result={})
    assert [event["event_id"] for event in first] == [event["event_id"] for event in second]


def test_runtime_events_do_not_persist_raw_exception_details():
    events = _runtime_events(
        [{
            "type": "error",
            "error": "provider api_key=super-secret host=10.0.0.7",
            "exception": "Traceback (most recent call last): ...",
            "token": "lease-secret",
        }],
        invocation_id=item().invocation_id,
        target="failed",
        result={
            "error_code": "BRAIN_EXCEPTION",
            "error_message": "SQL password=super-secret at mysql.internal",
        },
    )
    payload = events[0]["payload"]
    completed = events[-1]["payload"]
    assert payload["error_code"] == "BRAIN_ERROR"
    assert "error" not in payload
    assert "exception" not in payload
    assert "token" not in payload
    assert "super-secret" not in repr(events)
    assert "mysql.internal" not in repr(events)
    assert completed["error_code"] == "BRAIN_EXCEPTION"
    assert "error_message" not in completed


def test_normalize_outcome_replaces_untrusted_error_text_with_stable_message():
    status, events, result = _normalize_investigation_outcome({
        "status": "failed",
        "error_code": "not a stable code: provider api_key=secret",
        "error_message": "password=secret host=10.0.0.8",
        "events": [],
    })
    assert status == "failed"
    assert events == []
    assert result["error_code"] == "BRAIN_ERROR"
    assert result["error_message"] == "investigation failed"
    assert "secret" not in repr(result)


def test_error_safety_contract_has_no_raw_llm_or_stream_error_text():
    from pathlib import Path

    source = (Path(__file__).resolve().parents[1] / "orchestrator.py").read_text()
    assert 'return f"[LLM error: {e}]"' not in source
    assert "分析执行异常: {err_detail}" not in source
