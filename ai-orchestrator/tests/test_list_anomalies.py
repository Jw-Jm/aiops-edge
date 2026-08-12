"""list_anomalies 查 MySQL anomaly_events 表的单元测试。"""
import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))


def _make_app():
    """加载 main 模块（隔离 AIOPS_DATA_DIR，避免 /var/lib/aiops 写权限问题）。"""
    os.environ.setdefault("AIOPS_DATA_DIR", "/tmp/aiops-test-list-anomalies")
    import main
    return main


def test_list_anomalies_queries_mysql_when_available():
    """db 可用时，list_anomalies 应 SELECT anomaly_events 并返回行。"""
    main = _make_app()
    from unittest.mock import patch, MagicMock

    fake_rows = [
        {"service_name": "frontend", "metric": "error_rate", "value": 5.2,
         "method": "3sigma", "severity": "critical", "score": 0.91,
         "detected_at": "2026-08-11 10:00:00"},
        {"service_name": "frontend", "metric": "p99_latency", "value": 880.0,
         "method": "iqr", "severity": "warning", "score": 0.55,
         "detected_at": "2026-08-11 09:55:00"},
    ]
    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.fetchall.return_value = fake_rows
    mock_conn.cursor.return_value.__enter__.return_value = mock_cursor

    # list_anomalies 内部用 `from db import db_available, get_conn` 局部导入，
    # 所以 patch 源模块 db.db_available / db.get_conn（import 发生在调用时，会取到 mock）
    with patch('db.db_available', return_value=True), \
         patch('db.get_conn', return_value=mock_conn):
        # main.list_anomalies 是 async def，用 asyncio.run 驱动
        import asyncio
        result = asyncio.run(main.list_anomalies(service="frontend", limit=10))

    assert result["total"] == 2
    assert result["anomaly_trends"] == fake_rows
    # 校验 SQL 走了 anomaly_events 表 + service 过滤 + LIMIT
    sql_arg = mock_cursor.execute.call_args[0][0]
    assert "FROM anomaly_events" in sql_arg
    assert "WHERE service_name=%s" in sql_arg
    assert "ORDER BY detected_at DESC" in sql_arg
    args = mock_cursor.execute.call_args[0][1]
    assert args == ["frontend", 10]
    mock_conn.close.assert_called_once()


def test_list_anomalies_degrades_when_db_unavailable():
    """db 不可用时，list_anomalies 应返回空列表 + total 0，不抛异常。"""
    main = _make_app()
    from unittest.mock import patch
    import asyncio

    with patch('db.db_available', return_value=False):
        result = asyncio.run(main.list_anomalies())
    assert result == {"anomaly_trends": [], "total": 0}


def test_list_anomalies_no_service_filter_no_where():
    """无 service 参数时，SQL 不应带 WHERE 子句。"""
    main = _make_app()
    from unittest.mock import patch, MagicMock
    import asyncio

    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.fetchall.return_value = []
    mock_conn.cursor.return_value.__enter__.return_value = mock_cursor

    with patch('db.db_available', return_value=True), \
         patch('db.get_conn', return_value=mock_conn):
        asyncio.run(main.list_anomalies(limit=50))

    sql_arg = mock_cursor.execute.call_args[0][0]
    assert "WHERE" not in sql_arg
    assert "LIMIT %s" in sql_arg


def test_list_anomalies_limit_capped_at_500():
    """limit=999999 应被截断为 500，不报错（防止超大分页查询）。"""
    main = _make_app()
    from unittest.mock import patch, MagicMock
    import asyncio

    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.fetchall.return_value = []
    mock_conn.cursor.return_value.__enter__.return_value = mock_cursor

    with patch('db.db_available', return_value=True), \
         patch('db.get_conn', return_value=mock_conn):
        # limit=999999 → SQL 里的 LIMIT 参数应为 500
        asyncio.run(main.list_anomalies(limit=999999))

    args = mock_cursor.execute.call_args[0][1]
    assert args[-1] == 500, f"limit should be capped at 500, got {args}"


def test_list_anomalies_limit_min_one():
    """limit=0 或负数应被抬高为至少 1，不产生非法 LIMIT。"""
    main = _make_app()
    from unittest.mock import patch, MagicMock
    import asyncio

    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.fetchall.return_value = []
    mock_conn.cursor.return_value.__enter__.return_value = mock_cursor

    with patch('db.db_available', return_value=True), \
         patch('db.get_conn', return_value=mock_conn):
        asyncio.run(main.list_anomalies(limit=0))

    args = mock_cursor.execute.call_args[0][1]
    assert args[-1] >= 1, f"limit should be at least 1, got {args}"
