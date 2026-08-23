"""P9.3 Support Matcher — V9.3 Phase9（TDD RED 测试）。

把 Evidence→Hypothesis 的支持关系结构化，计算 evidence support；
同一 provenance 重复 Evidence 不重复加权（§三十八）。
direct_support=1.0, indirect_support=0.6, top 5 unique supporting evidence。
"""
import pytest


def test_support_relation_direct_and_indirect():
    from support_matcher import SupportMatcher

    matcher = SupportMatcher()
    # 两个 evidence 支持同一 hypothesis
    matcher.add_relation(
        hypothesis_id="h-1",
        evidence_id="ev-1",
        source_reliability=0.95,
        relation="direct_support",
    )
    matcher.add_relation(
        hypothesis_id="h-1",
        evidence_id="ev-2",
        source_reliability=0.85,
        relation="indirect_support",
    )
    support = matcher.evidence_support("h-1")
    # (0.95*1.0 + 0.85*0.6) / (0.95+0.85)
    expected = (0.95 * 1.0 + 0.85 * 0.6) / (0.95 + 0.85)
    assert support == pytest.approx(expected, abs=1e-9)


def test_same_provenance_evidence_not_double_counted():
    from support_matcher import SupportMatcher

    matcher = SupportMatcher()
    # 同一 evidence 被重复添加 → 只算一次（provenance 去重）
    matcher.add_relation(hypothesis_id="h-1", evidence_id="ev-1",
                         source_reliability=0.95, relation="direct_support")
    matcher.add_relation(hypothesis_id="h-1", evidence_id="ev-1",
                         source_reliability=0.95, relation="direct_support")
    support = matcher.evidence_support("h-1")
    assert support == pytest.approx(1.0, abs=1e-9)


def test_support_uses_top5_unique():
    from support_matcher import SupportMatcher

    matcher = SupportMatcher()
    # 6 个 unique evidence → 只用 top 5
    for i in range(6):
        matcher.add_relation(
            hypothesis_id="h-1",
            evidence_id=f"ev-{i}",
            source_reliability=0.95,
            relation="direct_support",
        )
    support = matcher.evidence_support("h-1")
    # top5 全部 0.95 direct（relation_weight=1.0）→ support = Σ(0.95*1.0)/Σ(0.95) = 1.0
    assert support == pytest.approx(1.0, abs=1e-9)


def test_support_missing_hypothesis_is_zero():
    from support_matcher import SupportMatcher

    matcher = SupportMatcher()
    assert matcher.evidence_support("no-such-h") == 0.0


def test_support_relations_are_listable():
    from support_matcher import SupportMatcher

    matcher = SupportMatcher()
    matcher.add_relation(hypothesis_id="h-1", evidence_id="ev-1",
                         source_reliability=0.9, relation="direct_support")
    rels = matcher.relations_for("h-1")
    assert len(rels) == 1
    assert rels[0].evidence_id == "ev-1"
    assert rels[0].relation == "direct_support"
