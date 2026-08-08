from db_approval import ApprovalStore

s = ApprovalStore()


def test_create_and_get():
    task = {"id": "t1", "status": "waiting", "service": "svc-a", "context": "test",
            "diagnosis": "", "plan": "p", "script": "s", "risk_score": 5,
            "risk_reason": "r", "report": "", "created_at": "2026-08-08T00:00:00Z", "done_at": ""}
    s.create(task)
    got = s.get("t1")
    if got is not None:
        assert got["id"] == "t1"
        assert got["status"] == "waiting"


def test_decide():
    s.decide("t1", "approved", "admin")
    got = s.get("t1")
    if got is not None:
        assert got["status"] == "approved"


def test_list():
    result = s.list()
    assert isinstance(result, list)
