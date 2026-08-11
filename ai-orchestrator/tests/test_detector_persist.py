import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

def test_persist_anomaly_writes_to_mysql():
    """_persist_anomaly 应该调 MySQL INSERT"""
    from detector import AnomalyDetector
    from unittest.mock import patch, MagicMock

    detector = AnomalyDetector(window_size=10)

    # mock db 模块
    with patch('detector.db_available', return_value=True), \
         patch('detector.get_conn') as mock_get_conn:
        mock_conn = MagicMock()
        mock_cursor = MagicMock()
        mock_conn.cursor.return_value.__enter__.return_value = mock_cursor
        mock_get_conn.return_value = mock_conn

        confirmed = MagicMock()
        confirmed.service = "frontend"
        confirmed.metric = "error_rate"
        confirmed.current_value = 5.0
        confirmed.method = "zscore"
        confirmed.severity = "critical"
        confirmed.score = 0.95

        detector._persist_anomaly(confirmed)

        # 验证 INSERT 被调用
        assert mock_cursor.execute.called, "INSERT was not called"
        sql_arg = mock_cursor.execute.call_args[0][0]
        assert "INSERT INTO anomaly_events" in sql_arg, f"wrong SQL: {sql_arg}"
