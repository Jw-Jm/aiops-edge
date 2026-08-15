from nl2sql import validate_sql, normalize_sql, Nl2SqlStore

ALLOWED_TABLES = {"observability.trace_spans", "observability.service_topology",
                  "observability.log_records"}


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


# ── P1-1 安全加固：表函数 / INTO OUTFILE / 裸表名 ─────────────────────────

def test_reject_table_functions():
    """ClickHouse 表函数（SSRF/出网/落盘/CPU 放大）必须被拒绝。"""
    bad = [
        "SELECT * FROM file('/etc/passwd')",
        "SELECT count() FROM url('http://attacker.example/x', 'CSV')",
        "SELECT * FROM remote('evil-host:9000', 'db.tbl')",
        "SELECT * FROM remoteSecure('evil-host:9440', 'db.tbl')",
        "SELECT * FROM mysql('db-host:3306', 'db', 'tbl', 'u', 'p')",
        "SELECT * FROM postgresql('db-host:5432', 'db', 'tbl', 'u', 'p')",
        "SELECT * FROM mongodb('db-host:27017', 'db.tbl', 'u', 'p')",
        "SELECT * FROM jdbc('jdbc:clickhouse://db-host:8123', 'db', 'tbl')",
        "SELECT * FROM s3('https://bucket.s3.amazonaws.com/obj', 'CSV')",
        "SELECT * FROM hdfs('hdfs://host:8020/path')",
        "SELECT * FROM gcs('https://storage.googleapis.com/bucket/obj')",
        "SELECT * FROM numbers(1000000000)",
        "SELECT * FROM generateRandom('b UInt8', 1000000, 10)",
    ]
    for sql in bad:
        assert not validate_sql(sql, ALLOWED_TABLES), f"表函数应被拒绝: {sql}"


def test_reject_table_function_case_insensitive():
    """表函数关键字大小写不敏感。"""
    for sql in ("SELECT * FROM FILE('/etc/passwd')",
                "SELECT count() FROM URL('http://x', 'CSV')",
                "SELECT * FROM Numbers(10)",
                "SELECT * FROM GENERATERANDOM()"):
        assert not validate_sql(sql, ALLOWED_TABLES), f"大小写变体应被拒绝: {sql}"


def test_reject_into_outfile():
    """INTO OUTFILE（落盘）必须被拒绝（大小写不敏感、多空格容忍）。"""
    assert not validate_sql(
        "SELECT * FROM observability.trace_spans INTO OUTFILE '/tmp/x.csv'", ALLOWED_TABLES)
    assert not validate_sql(
        "SELECT * FROM observability.trace_spans INTO  OUTFILE '/tmp/x.csv'", ALLOWED_TABLES)
    assert not validate_sql(
        "SELECT * FROM observability.trace_spans into outfile '/tmp/x.csv'", ALLOWED_TABLES)


def test_reject_bare_table_name():
    """无库前缀的裸表名（FROM 表名）必须被拒绝，防绕过硬白名单。"""
    assert not validate_sql("SELECT * FROM trace_spans LIMIT 10", ALLOWED_TABLES)
    assert not validate_sql("SELECT * FROM log_records LIMIT 10", ALLOWED_TABLES)
    assert not validate_sql("SELECT a.* FROM service_topology a LIMIT 10", ALLOWED_TABLES)
    assert not validate_sql("SELECT * FROM observability.trace_spans a "
                            "JOIN log_records b ON a.service_name=b.service_name LIMIT 10",
                            ALLOWED_TABLES)


def test_reject_quoted_table_name():
    """反引号/引号包裹的表引用可绕过字符白名单，必须拒绝。"""
    assert not validate_sql("SELECT * FROM `trace_spans` LIMIT 10", ALLOWED_TABLES)
    assert not validate_sql("SELECT * FROM `observability.trace_spans` LIMIT 10", ALLOWED_TABLES)


def test_accept_qualified_tables_still_valid():
    """带库前缀的白名单表查询仍应通过（回归保护）。"""
    assert validate_sql(
        "SELECT service_name, count() AS calls FROM observability.trace_spans "
        "WHERE start_time >= now() - INTERVAL 1 HOUR GROUP BY service_name ORDER BY calls DESC LIMIT 10",
        ALLOWED_TABLES)
    assert validate_sql(
        "SELECT source_service, destination_service, calls, error_rate "
        "FROM observability.service_topology WHERE time_bucket >= now() - INTERVAL 24 HOUR "
        "ORDER BY calls DESC LIMIT 10", ALLOWED_TABLES)
    assert validate_sql(
        "SELECT service_name, count() AS logs FROM observability.log_records "
        "WHERE timestamp >= now() - INTERVAL 24 HOUR GROUP BY service_name ORDER BY logs DESC LIMIT 10",
        ALLOWED_TABLES)


def test_store_roundtrip():
    s = Nl2SqlStore()
    sid = s.save({"id": "abc123", "sql": "SELECT 1", "explanation": "测试"})
    item = s.get(sid)
    assert item is not None and item["status"] == "pending"
    s.mark_executed(sid)
    assert s.get(sid)["status"] == "executed"


def test_fallback_nl2sql_error_rate_ordering():
    """P1-4: 错误类 fallback SQL 按错误率(countIf 比例)排序、LIMIT 10、时间窗口正确。"""
    pytest = __import__("pytest")
    try:
        from main import _fallback_nl2sql
    except Exception as e:  # main 依赖环境（DB 目录/可选依赖），不可用时跳过
        pytest.skip(f"main 不可导入: {e}")
    sql = _fallback_nl2sql("近1小时错误率最高的服务有哪些")
    assert "error_rate" in sql, f"应输出错误率字段: {sql}"
    assert "ORDER BY error_rate DESC" in sql, f"应按错误率排序: {sql}"
    assert "LIMIT 10" in sql, f"应 LIMIT 10 聚焦 Top: {sql}"
    assert "INTERVAL 1 HOUR" in sql, f"应解析时间窗口近1小时: {sql}"
    # 生成的 fallback SQL 必须能通过安全校验（白名单表 + SELECT + 无多语句）
    assert validate_sql(sql), f"fallback SQL 必须通过 validate_sql: {sql}"


def test_fallback_nl2sql_default_window_and_limit():
    """P1-4/P1-1: 未指定时间时默认近 24h；所有 fallback SQL 均有 LIMIT 护栏且通过校验。"""
    pytest = __import__("pytest")
    try:
        from main import _fallback_nl2sql
    except Exception as e:
        pytest.skip(f"main 不可导入: {e}")
    for q in ("日志量最多的服务", "拓扑调用关系", "延迟最高的服务", "服务调用量"):
        sql = _fallback_nl2sql(q)
        assert validate_sql(sql), f"fallback SQL 校验失败 ({q}): {sql}"
        assert "LIMIT" in sql
        if "日志" in q or "拓扑" in q or "延迟" in q:
            assert "INTERVAL 24 HOUR" in sql, f"默认窗口应为近24h ({q}): {sql}"
