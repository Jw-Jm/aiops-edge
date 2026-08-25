"""Recoverable execution of one accepted, persisted Investigation Run."""

from __future__ import annotations

import uuid
from dataclasses import replace
from dataclasses import dataclass
from typing import Any

from control_plane_client import ControlPlaneError, ControlPlaneClient
from investigation_dispatcher import AcceptedInvocation
from lease_aware_execution import LeaseAwareExecutor


@dataclass
class AcceptedWork:
    invocation: AcceptedInvocation
    lease: Any
    state_version: int
    status: str = "planning"


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
        status = str(snapshot.get("status", ""))
        version = int(snapshot.get("state_version", 0))
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
            if self.brain is None:
                events = []
            else:
                events = await self.brain.investigate(item, work.lease)
            work.lease.check_active()
            if work.status != "verifying":
                response = self.control_plane.transition(
                    run_id=item.run_id, target="verifying", expected_version=version,
                    tenant_id=item.tenant_id, command_id=str(uuid.uuid4()),
                )
                version = int(self._run(response).get("state_version", version + 1))
            work.lease.commit(
                target="success", result={"events": len(events)},
                events=[{"event_type": "run.completed", "payload": {"events": len(events)}}],
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
