"""P9.10 Unknown-safe Handler — V9.3 Phase9（TDD RED 测试）。

无法达到阈值或 critical evidence 缺失时：
  root_cause = unknown
  missing_evidence = explicit
  no automatic remediation
（§七十五 P9.10，F5：P9 不触发执行）
"""
import pytest


def test_unknown_when_threshold_not_met():
    from unknown_handler import UnknownSafeHandler

    handler = UnknownSafeHandler()
    result = handler.handle(
        run_id="run-1",
        root_cause=None,  # 无 confirmed root cause
        missing_evidence=["trace_anomaly"],
    )
    assert result.root_cause == "unknown"
    assert result.missing_evidence == ["trace_anomaly"]


def test_missing_evidence_explicit_not_hidden():
    from unknown_handler import UnknownSafeHandler

    handler = UnknownSafeHandler()
    result = handler.handle(
        run_id="run-1",
        root_cause=None,
        missing_evidence=["k8s_state", "metric_anomaly"],
    )
    # missing 显式列出，不通过语言润色掩盖
    assert result.missing_evidence == ["k8s_state", "metric_anomaly"]
    assert result.explicit_missing is True


def test_no_automatic_remediation():
    from unknown_handler import UnknownSafeHandler

    handler = UnknownSafeHandler()
    result = handler.handle(
        run_id="run-1",
        root_cause=None,
        missing_evidence=["log_error"],
    )
    # P9 不产生任何执行动作（F5）
    assert result.automatic_remediation is False
    assert len(result.ops_actions) == 0


def test_unknown_is_explicit_valid_state():
    from unknown_handler import UnknownSafeHandler

    handler = UnknownSafeHandler()
    result = handler.handle(run_id="run-1", root_cause=None, missing_evidence=[])
    assert result.root_cause == "unknown"
    assert result.is_unknown is True


def test_known_root_cause_passthrough():
    from unknown_handler import UnknownSafeHandler

    handler = UnknownSafeHandler()
    result = handler.handle(
        run_id="run-1",
        root_cause="h-1",
        missing_evidence=[],
    )
    assert result.root_cause == "h-1"
    assert result.is_unknown is False
