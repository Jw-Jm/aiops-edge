"""Deterministic cross-service contract gate for the canonical Action path.

The disposable harness models the durable boundaries without contacting a
cluster, a provider LLM, or a real executor. It catches duplicate approval and
mutation attempts when a response is lost.
"""

from dataclasses import dataclass

import pytest


@dataclass
class Harness:
    action_version: int = 1
    action_hash: str = "hash-v2"
    decision_key: str | None = None
    decision: str | None = None
    execution: str = "queued"
    executor_calls: int = 0
    reconcile_calls: int = 0

    def decide(self, key: str, decision: str, version: int) -> str:
        if version != self.action_version:
            raise ValueError("STALE_ACTION_VERSION")
        if self.decision_key is not None:
            if (key, decision) != (self.decision_key, self.decision):
                raise ValueError("IDEMPOTENCY_KEY_REUSED")
            return "replay"
        self.decision_key, self.decision = key, decision
        self.execution = "queued" if decision == "approved" else "rejected"
        return "created"

    def dispatch(self, response_lost: bool = False) -> None:
        if self.execution == "execution_unknown":
            self.reconcile_calls += 1
            self.execution = "success"
            return
        if self.execution != "queued":
            return
        self.executor_calls += 1
        self.execution = "execution_unknown" if response_lost else "success"


def test_approval_and_lost_response_are_idempotent():
    h = Harness()
    assert h.decide("d1", "approved", 1) == "created"
    assert h.decide("d1", "approved", 1) == "replay"
    h.dispatch(response_lost=True)
    h.dispatch()
    h.dispatch()  # terminal replay must not mutate again
    assert h.executor_calls == 1
    assert h.reconcile_calls == 1
    assert h.execution == "success"


def test_changed_request_cannot_reuse_decision_key():
    h = Harness()
    h.decide("d1", "approved", 1)
    with pytest.raises(ValueError, match="IDEMPOTENCY_KEY_REUSED"):
        h.decide("d1", "rejected", 1)
    with pytest.raises(ValueError, match="STALE_ACTION_VERSION"):
        Harness().decide("d1", "approved", 2)
