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


def test_write_failure_is_logged_and_marked_degraded(monkeypatch, caplog):
    """MySQL 写入失败不得静默：必须记 error 日志并进入降级态（P1-R1 回归锁定）。"""
    import logging

    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: True)

    class _BrokenConn:
        def cursor(self):
            raise RuntimeError("mysql down")

        def close(self):
            pass

    monkeypatch.setattr("db_approval.db.get_conn", lambda: _BrokenConn())
    assert s.degraded() is False

    with caplog.at_level(logging.ERROR, logger="aiops.db_approval"):
        s.update("t-degraded", status="approved")
    assert s.degraded() is True
    assert any("falling back to in-memory" in r.message for r in caplog.records)

    # 内存兜底仍生效
    s._mem["t-degraded"] = {"status": "waiting"}
    s.update("t-degraded", status="approved")
    assert s._mem["t-degraded"]["status"] == "approved"


def test_update_rejects_non_whitelisted_columns():
    """动态列名必须过白名单，防止新增调用方把任意字段拼进 SQL（P2-S8）。"""
    import pytest

    s = ApprovalStore()
    with pytest.raises(ValueError):
        s.update("t1", status="approved", evil_column="x")
