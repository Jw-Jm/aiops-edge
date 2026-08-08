from nl2sql import validate_sql, normalize_sql, Nl2SqlStore

ALLOWED_TABLES = {"observability.trace_spans", "observability.service_topology",
                  "observability.log_records", "observability.inspection_reports"}


def test_reject_insert():
    assert not validate_sql("INSERT INTO observability.trace_spans VALUES (1)", ALLOWED_TABLES)


def test_reject_blacklisted_table():
    assert not validate_sql("SELECT * FROM observability.secrets", ALLOWED_TABLES)


def test_reject_multi_statement():
    assert not validate_sql("SELECT 1; DROP TABLE x", ALLOWED_TABLES)


def test_force_limit():
    assert "LIMIT" in normalize_sql("SELECT * FROM observability.trace_spans", ALLOWED_TABLES)


def test_accept_valid():
    assert validate_sql("SELECT count() FROM observability.trace_spans WHERE is_error=1 LIMIT 10", ALLOWED_TABLES)


def test_reject_update():
    assert not validate_sql("UPDATE observability.trace_spans SET is_error=0", ALLOWED_TABLES)


def test_store_roundtrip():
    s = Nl2SqlStore()
    sid = s.save({"id": "abc123", "sql": "SELECT 1", "explanation": "测试"})
    item = s.get(sid)
    assert item is not None and item["status"] == "pending"
    s.mark_executed(sid)
    assert s.get(sid)["status"] == "executed"
