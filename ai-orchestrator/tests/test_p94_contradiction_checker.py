"""P9.4 Contradiction Checker — V9.3 Phase9（TDD RED 测试）。

主动搜索反证：时间矛盾、资源/cluster 矛盾、指标与日志/trace 矛盾、变更发生在故障后等反证（§七十五 P9.4）。
unresolved critical contradiction → 不得 confirmed，无论 score 多高（§四十铁律）。
"""
import pytest


def test_detects_time_conflict_critical():
    from contradiction_checker import ContradictionChecker, Contradiction

    checker = ContradictionChecker()
    checker.add_contradiction(
        hypothesis_id="h-1",
        evidence_id="ev-x",
        contradiction_type="time_conflict",
        severity="critical",
        description="候选事件发生在异常之后",
    )
    unresolved = checker.unresolved_critical("h-1")
    assert len(unresolved) == 1
    assert unresolved[0].contradiction_type == "time_conflict"


def test_detects_change_after_fault():
    from contradiction_checker import ContradictionChecker

    checker = ContradictionChecker()
    checker.add_contradiction(
        hypothesis_id="h-1",
        evidence_id="ev-c",
        contradiction_type="change_after_fault",
        severity="critical",
        description="变更发生在故障后",
    )
    assert checker.has_unresolved_critical("h-1") is True


def test_cross_cluster_contradiction_normal():
    from contradiction_checker import ContradictionChecker

    checker = ContradictionChecker()
    checker.add_contradiction(
        hypothesis_id="h-1",
        evidence_id="ev-r",
        contradiction_type="resource_cluster_conflict",
        severity="normal",
        description="资源归属不同 cluster",
    )
    # normal 不阻断 confirmed，但会 penalty
    assert checker.has_unresolved_critical("h-1") is False
    assert len(checker.all_contradictions("h-1")) == 1


def test_resolved_contradiction_not_counted():
    from contradiction_checker import ContradictionChecker

    checker = ContradictionChecker()
    checker.add_contradiction(
        hypothesis_id="h-1",
        evidence_id="ev-x",
        contradiction_type="metric_log_trace_conflict",
        severity="critical",
        description="已解决",
    )
    checker.resolve("h-1", evidence_id="ev-x")
    assert checker.has_unresolved_critical("h-1") is False


def test_unresolved_critical_blocks_confirmation():
    from contradiction_checker import ContradictionChecker

    checker = ContradictionChecker()
    checker.add_contradiction(
        hypothesis_id="h-1",
        evidence_id="ev-t",
        contradiction_type="temporal_relation_weak",
        severity="critical",
        description="时间关系无支持",
    )
    # confirmed 需要 no unresolved critical contradiction
    assert checker.blocks_confirmation("h-1") is True


def test_contradiction_validation_rejects_unknown_type():
    from contradiction_checker import ContradictionChecker

    checker = ContradictionChecker()
    with pytest.raises(ValueError):
        checker.add_contradiction(
            hypothesis_id="h-1",
            evidence_id="ev-x",
            contradiction_type="bogus_type",
            severity="critical",
            description="非法类型",
        )
