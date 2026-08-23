"""P9.5 Missing Evidence Engine — V9.3 Phase9（TDD RED 测试）。

每条 hypothesis 明确 critical/optional missing。critical missing 会限制最终状态，
不得通过语言润色掩盖（§七十五 P9.5）。
reason 复用 claim_type=unknown 冻结枚举（insufficient_data/permission_denied/unavailable_source/expired_evidence）。
"""
import pytest


def test_missing_evidence_critical_optional():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    eng.add_missing(
        hypothesis_id="h-1",
        required_type="metric_anomaly",
        critical=True,
        reason="insufficient_data",
    )
    eng.add_missing(
        hypothesis_id="h-1",
        required_type="log_pattern",
        critical=False,
        reason="insufficient_data",
    )
    critical = eng.critical_missing("h-1")
    optional = eng.optional_missing("h-1")
    assert len(critical) == 1 and critical[0].required_type == "metric_anomaly"
    assert len(optional) == 1 and optional[0].required_type == "log_pattern"


def test_critical_missing_limits_final_state():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    eng.add_missing(
        hypothesis_id="h-1",
        required_type="trace_anomaly",
        critical=True,
        reason="insufficient_data",
    )
    # 存在 critical missing → 无法 confirmed / 自动补救
    assert eng.has_critical_missing("h-1") is True
    assert eng.blocks_confirmation("h-1") is True


def test_optional_missing_does_not_block():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    eng.add_missing(
        hypothesis_id="h-1",
        required_type="log_pattern",
        critical=False,
        reason="insufficient_data",
    )
    assert eng.has_critical_missing("h-1") is False
    assert eng.blocks_confirmation("h-1") is False


def test_reason_uses_frozen_enum():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    with pytest.raises(ValueError):
        eng.add_missing(
            hypothesis_id="h-1",
            required_type="metric_anomaly",
            critical=True,
            reason="bogus_reason",
        )


def test_missing_evidence_followup_slot():
    from missing_evidence import MissingEvidenceEngine

    eng = MissingEvidenceEngine()
    eng.add_missing(
        hypothesis_id="h-1",
        required_type="k8s_state",
        critical=True,
        reason="unavailable_source",
        followup_slot="tool:query_k8s",
    )
    m = eng.critical_missing("h-1")[0]
    assert m.followup_slot == "tool:query_k8s"
