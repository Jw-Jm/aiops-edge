import asyncio

from verification_worker import VerificationCandidate, VerificationWorker


class FakeControlPlane:
    def __init__(self):
        self.verifications = []
        self.commits = []

    def append_verification(self, **payload):
        self.verifications.append(payload)
        return {"created": True}

    def commit(self, **payload):
        self.commits.append(payload)
        return payload


def candidate(before=None, operation="scale", spec=None):
    return VerificationCandidate(
        run_id="11111111-1111-4111-8111-111111111111",
        tenant_id="tenant-1", action_id="action-1", action_hash="hash-1",
        operation=operation, target_uid="uid-1", target_spec=spec or {"replicas": 2},
        before=before or {"error_rate": 0.01, "p95_latency_ms": 100},
    )


def test_passed_verification_commits_success():
    cp = FakeControlPlane()
    worker = VerificationWorker(lambda _: {"uid": "uid-1", "replicas": 2, "error_rate": 0.01, "p95_latency_ms": 90}, cp)
    c = candidate()
    outcome = worker.verify(c)
    assert outcome.status == "passed"
    result = worker.persist_and_commit(c, outcome, {"uid": "uid-1", "replicas": 2}, expected_version=3, owner_id="v", epoch=1, token="t")
    assert result["target"] == "success"
    assert cp.verifications[-1]["status"] == "passed"


def test_regressed_verification_never_commits_success():
    cp = FakeControlPlane()
    worker = VerificationWorker(lambda _: {"uid": "uid-1", "replicas": 2, "error_rate": 0.10, "p95_latency_ms": 100}, cp)
    c = candidate()
    outcome = worker.verify(c)
    assert outcome.status == "regressed"
    worker.persist_and_commit(c, outcome, {"uid": "uid-1", "replicas": 2}, expected_version=3, owner_id="v", epoch=1, token="t")
    assert cp.commits[-1]["target"] == "regressed"


def test_missing_post_window_is_inconclusive():
    cp = FakeControlPlane()
    worker = VerificationWorker(lambda _: {}, cp)
    c = candidate()
    outcome = worker.verify(c)
    assert outcome.status == "inconclusive"
    result = worker.persist_and_commit(c, outcome, {})
    assert result["terminal_status"] == "partial"
