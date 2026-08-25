"""Independent, read-only Action verification worker.

The worker deliberately has no executor client or mutation credential.  It
accepts frozen before/after observations, derives a verdict from target
identity, desired state and SLI checks, then persists the result through the
control-plane client.  A missing observer result is always inconclusive.
"""
from __future__ import annotations

import hashlib
import json
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Mapping


@dataclass(frozen=True)
class VerificationCandidate:
    run_id: str
    tenant_id: str
    action_id: str
    action_hash: str
    operation: str
    target_uid: str
    target_spec: Mapping[str, Any]
    before: Mapping[str, Any]


@dataclass(frozen=True)
class VerificationOutcome:
    verification_id: str
    status: str
    checks: tuple[Mapping[str, Any], ...]
    summary: str


def _payload_hash(candidate: VerificationCandidate, after: Mapping[str, Any], checks: list[Mapping[str, Any]]) -> str:
    payload = {
        "action_id": candidate.action_id,
        "action_hash": candidate.action_hash,
        "before": candidate.before,
        "after": after,
        "checks": checks,
    }
    raw = json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(raw).hexdigest()


def _desired_state_matches(candidate: VerificationCandidate, after: Mapping[str, Any]) -> bool:
    if after.get("uid") != candidate.target_uid:
        return False
    if candidate.operation == "scale":
        return after.get("replicas") == candidate.target_spec.get("replicas")
    if candidate.operation == "patch":
        desired = (candidate.target_spec.get("metadata") or {}).get("annotations") or {}
        observed = after.get("annotations") or {}
        return bool(desired) and all(observed.get(k) == v for k, v in desired.items())
    return False


class VerificationWorker:
    """Lease ownership is supplied by the caller; this class only verifies."""

    def __init__(self, observer: Callable[[VerificationCandidate], Mapping[str, Any]], control_plane: Any):
        self._observer = observer
        self._control_plane = control_plane

    def verify(self, candidate: VerificationCandidate) -> VerificationOutcome:
        try:
            observed = dict(self._observer(candidate) or {})
        except Exception as exc:  # observer failure is unknown-safe
            observed = {"observer_error": str(exc)}
        verification_id = str(uuid.uuid5(uuid.UUID(candidate.run_id), f"verification:{candidate.action_id}"))
        if not observed or observed.get("observer_error"):
            return VerificationOutcome(verification_id, "inconclusive", tuple(), "post-action observer unavailable")

        checks: list[Mapping[str, Any]] = []
        if observed.get("uid") != candidate.target_uid:
            checks.append({"target_identity_match": False, "effect_size": 0.0})
            return VerificationOutcome(verification_id, "regressed", tuple(checks), "target UID changed during verification")

        if not _desired_state_matches(candidate, observed):
            checks.append({"desired_state_match": False, "effect_size": 0.0})
            return VerificationOutcome(verification_id, "failed", tuple(checks), "target state does not match approved Action")

        before_error = candidate.before.get("error_rate")
        after_error = observed.get("error_rate")
        before_latency = candidate.before.get("p95_latency_ms")
        after_latency = observed.get("p95_latency_ms")
        side_effect = bool(observed.get("side_effect"))
        if before_error is not None and after_error is not None:
            side_effect = side_effect or float(after_error) > float(before_error) * 1.5
        if before_latency is not None and after_latency is not None:
            side_effect = side_effect or float(after_latency) > float(before_latency) * 1.5
        checks.append({
            "target_identity_match": True,
            "desired_state_match": True,
            "effect_size": 1.0,
            "side_effect": side_effect,
        })
        if side_effect:
            return VerificationOutcome(verification_id, "regressed", tuple(checks), "post-action observation detected a regression")
        return VerificationOutcome(verification_id, "passed", tuple(checks), "target and frozen observation windows match")

    def persist_and_commit(
        self,
        candidate: VerificationCandidate,
        outcome: VerificationOutcome,
        after: Mapping[str, Any],
        *,
        expected_version: int | None = None,
        owner_id: str = "",
        epoch: int = 0,
        token: str = "",
    ) -> dict:
        checks = [dict(item) for item in outcome.checks]
        payload_hash = _payload_hash(candidate, after, checks)
        self._control_plane.append_verification(
            run_id=candidate.run_id,
            tenant_id=candidate.tenant_id,
            verification_id=outcome.verification_id,
            action_id=candidate.action_id,
            status=outcome.status,
            before_snapshot=dict(candidate.before),
            after_snapshot=dict(after),
            observation_window_seconds=60,
            checks=checks,
            summary=outcome.summary,
        )
        terminal = {"passed": "success", "failed": "partial", "regressed": "regressed", "inconclusive": "partial"}[outcome.status]
        if expected_version is None:
            return {"status": outcome.status, "payload_hash": payload_hash, "terminal_status": terminal}
        return self._control_plane.commit(
            run_id=candidate.run_id,
            tenant_id=candidate.tenant_id,
            commit_id=outcome.verification_id,
            payload_hash=payload_hash,
            target=terminal,
            result={"verification_id": outcome.verification_id, "status": outcome.status},
            events=[
                {"event_id": outcome.verification_id, "event_type": "verification.completed", "payload": {"status": outcome.status}},
                {"event_id": str(uuid.uuid5(uuid.UUID(outcome.verification_id), "run.completed")), "event_type": "run.completed", "payload": {"status": terminal}},
            ],
            expected_version=expected_version,
            owner_id=owner_id,
            epoch=epoch,
            token=token,
        )
