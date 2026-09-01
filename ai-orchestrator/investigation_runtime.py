"""Recoverable execution of one accepted, persisted Investigation Run."""

from __future__ import annotations

import uuid
from dataclasses import replace
from dataclasses import dataclass
from typing import Any

from control_plane_client import ControlPlaneError, ControlPlaneClient
from error_safety import public_error_message, sanitize_runtime_payload, stable_error_code
from investigation_dispatcher import AcceptedInvocation
from invocation_scope import bind_execution_lease_token
from lease_aware_execution import LeaseAwareExecutor


@dataclass
class AcceptedWork:
    invocation: AcceptedInvocation
    lease: Any | None
    state_version: int
    status: str = "planning"


_TERMINAL_OUTCOMES = {"success", "partial", "failed", "regressed", "cancelled"}


def _normalize_investigation_outcome(raw: Any) -> tuple[str, list, dict]:
    """Normalize brain output without allowing errors to become success."""
    if isinstance(raw, dict):
        events = raw.get("events") if isinstance(raw.get("events"), list) else []
        status = str(raw.get("status") or "success").lower()
        result = dict(raw.get("result")) if isinstance(raw.get("result"), dict) else {}
        if raw.get("summary") is not None:
            result["summary"] = raw["summary"]
        # Error text is never part of the durable result contract.  Preserve a
        # stable code and a generic message so the UI/audit trail remains
        # actionable without exposing provider/SQL/credential details.
        raw_error_code = raw.get("error_code") or result.get("error_code")
        if raw_error_code is not None:
            result["error_code"] = stable_error_code(raw_error_code, "BRAIN_ERROR")
        result.pop("error_message", None)
    elif isinstance(raw, list):
        events = raw
        status = "success"
        result = {}
    else:
        events = []
        status = "failed"
        result = {"error_code": "INVALID_BRAIN_OUTCOME"}
    if status not in _TERMINAL_OUTCOMES | {"awaiting_approval", "awaiting_confirmation"}:
        status = "failed"
        result.setdefault("error_code", "INVALID_OUTCOME_STATUS")
    if any(isinstance(event, dict) and str(event.get("type") or event.get("event_type")) == "error"
           for event in events):
        if status == "success":
            status = "failed"
        result.setdefault("error_code", "BRAIN_ERROR_EVENT")
    if status in {"failed", "regressed"}:
        result["error_code"] = stable_error_code(result.get("error_code"), "BRAIN_ERROR")
    elif result.get("error_code"):
        result["error_code"] = stable_error_code(result["error_code"], "BRAIN_ERROR")
        result.pop("error_message", None)
    result = sanitize_runtime_payload(result)
    if status in {"failed", "regressed"}:
        result["error_message"] = public_error_message(result.get("error_code"), "investigation failed")
    result.setdefault("events", len(events))
    return status, events, result


def _stable_event_id(invocation_id: str, key: str) -> str:
    """Return a retry-stable UUID for a durable event slot."""
    try:
        namespace = uuid.UUID(str(invocation_id))
    except (ValueError, AttributeError):
        namespace = uuid.NAMESPACE_URL
    return str(uuid.uuid5(namespace, f"aiops:investigation:event:{key}"))


def _runtime_events(events: list, *, invocation_id: str, target: str, result: dict) -> list[dict]:
    out = []
    for index, event in enumerate(events):
        if isinstance(event, dict):
            event_type = str(event.get("event_type") or event.get("type") or "run.progress")
            raw_error = event.get("error_code") or event.get("error") or event.get("error_message")
            payload = sanitize_runtime_payload(event)
            if raw_error is not None:
                if event_type == "rca.error":
                    fallback = "RCA_V2_UNAVAILABLE"
                elif event_type == "tool_end":
                    fallback = "TOOL_FAILED"
                else:
                    fallback = "BRAIN_ERROR"
                payload["error_code"] = stable_error_code(event.get("error_code") or raw_error, fallback)
            supplied_id = str(event.get("event_id") or "")
        else:
            event_type = "run.progress"
            payload = {"value": str(event)[:4096]}
            supplied_id = ""
        try:
            event_id = str(uuid.UUID(supplied_id)) if supplied_id else ""
        except ValueError:
            event_id = ""
        out.append({"event_id": event_id or _stable_event_id(invocation_id, f"{index}:{event_type}:{supplied_id}"),
                    "event_type": event_type, "payload": payload})
    if target in _TERMINAL_OUTCOMES:
        safe_result = sanitize_runtime_payload(result)
        if isinstance(safe_result, dict) and safe_result.get("error_code"):
            safe_result["error_code"] = stable_error_code(safe_result["error_code"], "INTERNAL_ERROR")
        out.append({"event_id": _stable_event_id(invocation_id, f"completed:{target}"),
                    "event_type": "run.completed", "payload": {"status": target, **safe_result}})
    return out


class InvestigationRuntime:
    def __init__(self, *, control_plane: Any | None = None,
                 lease_executor: Any | None = None, brain: Any | None = None) -> None:
        self.control_plane = control_plane or ControlPlaneClient()
        self.lease_executor = lease_executor or LeaseAwareExecutor(self.control_plane)
        self.brain = brain

    @staticmethod
    def _run(response: dict) -> dict:
        return response.get("run") if isinstance(response.get("run"), dict) else response

    async def accept(self, item: AcceptedInvocation) -> AcceptedWork:
        snapshot = self._run(self.control_plane.get(run_id=item.run_id, tenant_id=item.tenant_id))
        # The control plane is the sole source of truth for RCA identity and
        # time bounds. Never derive a window from worker wall-clock time.
        # Recovery and older test doubles may omit the fields; production then
        # fails closed in the RCA adapter rather than silently drifting.
        frozen = dict(
            target_type=str(snapshot.get("target_type") or item.target_type or "service"),
            resource_id=str(snapshot.get("target_resource_id") or item.resource_id or ""),
            service=str(snapshot.get("target_resource_id") or item.service or item.resource_id or ""),
            window_start=str(snapshot.get("time_range_start") or item.window_start or ""),
            window_end=str(snapshot.get("time_range_end") or item.window_end or ""),
        )
        frozen["symptom_time"] = str(snapshot.get("symptom_time") or item.symptom_time or frozen["window_end"] or "")
        item = replace(item, **frozen)
        status = str(snapshot.get("status", ""))
        version = int(snapshot.get("state_version", 0))
        if status in {"success", "partial", "failed", "cancelled", "regressed"}:
            # The durable Run is already terminal.  Treat a redelivered outbox
            # message as an idempotent replay; do not claim a lease or enqueue
            # a second execution.
            return AcceptedWork(invocation=item, lease=None, state_version=version, status=status)
        if status not in {"created", "planning", "investigating", "verifying"}:
            raise RuntimeError(f"RUN_NOT_ACCEPTABLE:{status}")
        lease = self.lease_executor.acquire(
            run_id=item.run_id,
            tenant_id=item.tenant_id,
            owner_id=f"orchestrator:{item.invocation_id}",
            claim_id=item.invocation_id,
        )
        try:
            if status == "created":
                response = self.control_plane.transition(
                    run_id=item.run_id, target="planning", expected_version=version,
                    tenant_id=item.tenant_id, command_id=item.invocation_id,
                )
                version = int(self._run(response).get("state_version", version + 1))
            bound = item
            if getattr(item, "request_context", None) is not None and hasattr(lease, "_epoch"):
                context = item.request_context.bind_lease(
                    executor_id=f"orchestrator:{item.invocation_id}",
                    lease_epoch=int(getattr(lease, "_epoch", 0) or 0),
                    lease_token=str(getattr(lease, "_token", "") or ""),
                )
                bound = replace(item, request_context=context)
            return AcceptedWork(invocation=bound, lease=lease, state_version=version, status=status if status != "created" else "planning")
        except Exception:
            lease.close()
            raise

    async def execute(self, work: AcceptedWork) -> None:
        item = work.invocation
        try:
            work.lease.check_active()
            version = work.state_version
            if work.status == "planning":
                response = self.control_plane.transition(
                    run_id=item.run_id, target="investigating", expected_version=work.state_version,
                    tenant_id=item.tenant_id, command_id=str(uuid.uuid4()),
                )
                version = int(self._run(response).get("state_version", work.state_version + 1))
            elif work.status not in {"investigating", "verifying"}:
                raise RuntimeError(f"RUN_NOT_EXECUTABLE:{work.status}")
            try:
                if self.brain is None:
                    raw_outcome = []
                else:
                    # The lease token is process-local and intentionally never
                    # checkpointed into ScopeViewSnapshot.  Bind it only while
                    # the graph is executing so tools can authenticate calls
                    # without leaking the secret through recovery state.
                    with bind_execution_lease_token(str(getattr(work.lease, "_token", "") or "")):
                        raw_outcome = await self.brain.investigate(item, work.lease)
            except Exception as exc:  # noqa: BLE001 - persist truthful failure
                raw_outcome = {
                    "status": "failed", "error_code": "BRAIN_EXCEPTION",
                    "events": [],
                }
            target, events, result = _normalize_investigation_outcome(raw_outcome)
            work.lease.check_active()
            # Failure can be committed directly from investigating.  Successful
            # and partial outcomes must pass through verifying so the server-side
            # state machine retains a truthful evidence gate.
            if target in {"success", "partial", "regressed"} and work.status != "verifying":
                response = self.control_plane.transition(
                    run_id=item.run_id, target="verifying", expected_version=version,
                    tenant_id=item.tenant_id, command_id=str(uuid.uuid4()),
                )
                version = int(self._run(response).get("state_version", version + 1))
            # Mark the last RCA graph context final only at the same terminal
            # commit boundary.  This keeps active Runs on latest context while
            # making historical Run reads immutable and replayable.
            rca_payload = result.get("rca") if isinstance(result, dict) else None
            if target in _TERMINAL_OUTCOMES and isinstance(rca_payload, dict):
                context_payload = rca_payload.get("graph_context")
                append_context = getattr(self.control_plane, "append_graph_context", None)
                if isinstance(context_payload, dict) and callable(append_context):
                    try:
                        append_context(
                            run_id=item.run_id, tenant_id=item.tenant_id, cluster_id=item.cluster_id,
                            context_version=int(context_payload.get("context_version") or 1),
                            context=context_payload,
                            trigger_entity_uid=str(context_payload.get("symptom_entity_uid") or ""),
                            root_cause_entity_uid=str(rca_payload.get("root_cause") or ""),
                            is_final=True,
                            graph_generation=int(context_payload.get("graph_generation") or 0),
                            graph_schema_version=int(context_payload.get("graph_schema_version") or 2),
                        )
                    except Exception as exc:  # noqa: BLE001 - never claim durable RCA finality
                        target = "partial"
                        result["error_code"] = "GRAPH_CONTEXT_FINALIZE_FAILED"
                        result["error_message"] = public_error_message("GRAPH_CONTEXT_FINALIZE_FAILED")
            work.lease.commit(
                target=target, result=result,
                events=_runtime_events(events, invocation_id=item.invocation_id,
                                       target=target, result=result),
                expected_version=version,
            )
        finally:
            work.lease.close()

    async def fail(self, work: AcceptedWork, error: Exception) -> None:
        try:
            snapshot = self._run(self.control_plane.get(run_id=work.invocation.run_id,
                                                        tenant_id=work.invocation.tenant_id))
            status = str(snapshot.get("status", ""))
            version = int(snapshot.get("state_version", work.state_version))
            if status not in {"success", "partial", "failed", "cancelled", "regressed"}:
                self.control_plane.transition(
                    run_id=work.invocation.run_id, target="failed", expected_version=version,
                    tenant_id=work.invocation.tenant_id, command_id=str(uuid.uuid4()),
                )
        finally:
            # execute() normally closes the handle. This covers failures during
            # acceptance/worker startup without hiding the original error.
            if getattr(work.lease, "close", None):
                work.lease.close()

    async def reject(self, item: AcceptedWork, *, reason: str) -> None:
        if hasattr(item.lease, "close"):
            item.lease.close()
        self.control_plane.transition(
            run_id=item.invocation.run_id, target="failed", expected_version=item.state_version,
            tenant_id=item.invocation.tenant_id, command_id=str(uuid.uuid4()),
        )
