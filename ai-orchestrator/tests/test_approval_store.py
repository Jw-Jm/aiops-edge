import pytest

from db_approval import ApprovalStore, ApprovalStoreError


def _task(tid="t1", status="waiting"):
    return {"id": tid, "status": status, "service": "svc-a", "context": "test",
            "diagnosis": "", "plan": "p", "script": "s", "risk_score": 5,
            "risk_reason": "r", "report": "", "created_at": "2026-08-08T00:00:00Z", "done_at": ""}


class _RowConn:
    """fake MySQL connection：支持 cursor()/commit()/close()，SELECT 返回 dict 行。"""

    def __init__(self, rows=None):
        self._rows = rows or []

    def cursor(self):
        return _RowCursor(self._rows)

    def commit(self):
        pass

    def close(self):
        pass


class _RowCursor:
    def __init__(self, rows):
        self._rows = rows
        self.last = None

    def execute(self, sql, args=None):
        self.last = (sql, args)
        return 0

    def fetchone(self):
        return self._rows[0] if self._rows else None

    def fetchall(self):
        return self._rows

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class _BrokenConn:
    def cursor(self):
        raise RuntimeError("mysql down")

    def close(self):
        pass


def test_create_and_get_roundtrip(monkeypatch):
    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: True)
    rows = [{"task_id": "t1", "service_name": "svc-a", "status": "waiting", "plan": "p",
             "script": "s", "risk_score": 5, "risk_reason": "r", "diagnosis": "",
             "report": "", "requester": "", "created_at": "2026-08-08T00:00:00Z",
             "decided_at": None, "decision_by": None, "context": "", "source": ""}]
    monkeypatch.setattr("db_approval.db.get_conn", lambda: _RowConn(rows))
    s.create(_task())
    got = s.get("t1")
    assert got is not None and got["id"] == "t1" and got["status"] == "waiting"


def test_decide_updates_status(monkeypatch):
    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: True)
    monkeypatch.setattr("db_approval.db.get_conn", lambda: _RowConn())
    s.create(_task())
    s.decide("t1", "approved", "admin")
    assert s.degraded() is False


def test_write_failure_is_fail_closed(monkeypatch, caplog):
    """P1-R1: MySQL 写失败必须抛错（fail-closed），禁止内存降级充当授权。"""
    import logging

    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: True)
    monkeypatch.setattr("db_approval.db.get_conn", lambda: _BrokenConn())
    assert s.degraded() is False

    with caplog.at_level(logging.ERROR, logger="aiops.db_approval"), pytest.raises(ApprovalStoreError):
        s.update("t-degraded", status="approved")
    assert s.degraded() is True
    assert any("fail-closed" in r.message for r in caplog.records)


def test_create_unavailable_mysql_raises(monkeypatch):
    """MySQL 不可用：create 直接抛错，不做任何内存落库。"""
    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: False)
    with pytest.raises(ApprovalStoreError):
        s.create(_task())
    assert s.degraded() is True


def test_update_rejects_non_whitelisted_columns(monkeypatch):
    """动态列名必须过白名单（P2-S8）：无论 DB 可用性都拒绝。"""
    s = ApprovalStore()
    monkeypatch.setattr(s, "_available", lambda: True)
    monkeypatch.setattr("db_approval.db.get_conn", lambda: _RowConn())
    with pytest.raises(ValueError):
        s.update("t1", status="approved", evil_column="x")
