"""P9.7 Fixed Scoring Engine — V9.3 Phase9（评审加固版）。

严格使用冻结 V1 公式（§三十七）与 reliability（§三十六）；输出各分项和 penalty，结果可复算。
禁止 LLM 直接给最终 confidence 数字覆盖公式。

评审修复（2026-08-21 Gate 9 FAIL 退回）：
- 不再接受裸 support/reliability/temporal 数值（防调用方伪造分数）。
- 改为消费结构化 `RcaEvaluationInput`，内部从 Evidence/推导器计算全部分量：
  - evidence_support      ← SupportMatcher.evidence_support（§三十八）
  - source_reliability    ← unique supporting evidence reliability 均值（§三十八）
  - temporal_relation     ← Timeline.temporal_relation（§三十九）
  - critical/normal       ← ContradictionChecker（unresolved）
  - missing_critical      ← MissingEvidenceEngine.critical_missing
- score 前校验：所有 supporting evidence 必须已登记到 RcaInputSnapshot（禁未登记事实）。

base_score = llm_prior*0.35 + evidence_support*0.30 + source_reliability*0.20 + temporal_relation*0.15

Penalty（§三十七）:
  critical contradiction         -0.25 each, cap -0.50
  normal contradiction           -0.10 each, cap -0.30
  missing critical evidence      -0.20 each, cap -0.40

final_score = clamp(base_score - penalties, 0, 1)
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

# 冻结权重（§三十七，DeepSeek 不得调高）
WEIGHT_LLM_PRIOR = 0.35
WEIGHT_EVIDENCE_SUPPORT = 0.30
WEIGHT_SOURCE_RELIABILITY = 0.20
WEIGHT_TEMPORAL = 0.15

# 冻结 penalty（§三十七）
PENALTY_CRITICAL = 0.25
PENALTY_CRITICAL_CAP = 0.50
PENALTY_NORMAL = 0.10
PENALTY_NORMAL_CAP = 0.30
PENALTY_MISSING_CRITICAL = 0.20
PENALTY_MISSING_CAP = 0.40

# 无 timeline 时的默认 temporal（§三十九"时间关系弱"）
TEMPORAL_WEAK = 0.40


class ScoringError(ValueError):
    def __init__(self, message: str):
        self.error_code = "SCORING_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class Penalty:
    type: str
    value: float
    reason: str


@dataclass(frozen=True)
class ScoreBreakdown:
    hypothesis_id: str
    base_score: float
    components: Dict[str, float]
    penalties: List[Penalty]
    final_score: float

    def penalty_total(self) -> float:
        return sum(p.value for p in self.penalties)


@dataclass
class RcaEvaluationInput:
    """结构化 RCA 评估输入：只允许来自 Evidence/推导器，禁止外部直接注入分量。

    - snapshot: RcaInputSnapshot（强隔离 + 不可变）
    - hypothesis: Hypothesis（Phase 9 唯一正式实体）
    - support_matcher / contradiction_checker / missing_engine / timeline: 已推导组件
    - llm_reasoning_prior: 唯一 LLM 因子（§三十七 0.35 权重），仍受 0-1 约束，不覆盖公式
    """

    snapshot: Any
    hypothesis: Any
    support_matcher: Any
    contradiction_checker: Any
    missing_engine: Any
    timeline: Any = None
    llm_reasoning_prior: float = 0.0
    # timeline 时间推断所需的异常事件 ref（供 §三十九 temporal_relation 内部计算）
    abnormal_event_id: str = ""


class ScoringEngine:
    """Fixed Scoring Engine：唯一使用冻结 V1 公式计算 final_score。"""

    def score(self, input_: RcaEvaluationInput) -> ScoreBreakdown:
        if not isinstance(input_, RcaEvaluationInput):
            raise ScoringError("score 必须接收 RcaEvaluationInput（禁止直接传分量）")

        hypothesis_id = input_.hypothesis.hypothesis_id
        if not (0.0 <= input_.llm_reasoning_prior <= 1.0):
            raise ScoringError(f"llm_reasoning_prior 超出 [0,1]: {input_.llm_reasoning_prior}")

        # 校验：supporting evidence 必须已登记到 snapshot（禁未登记事实）
        for rel in input_.support_matcher.relations_for(hypothesis_id):
            input_.snapshot.assert_evidence_registered(rel.evidence_id)

        # 分量全部内部计算（§三十八/§三十九）
        evidence_support = input_.support_matcher.evidence_support(hypothesis_id)
        source_reliability = _unique_reliability_mean(input_.support_matcher, hypothesis_id)
        temporal_relation = _compute_temporal(input_)
        critical_count, normal_count = _contradiction_counts(input_.contradiction_checker, hypothesis_id)
        missing_critical_count = len(input_.missing_engine.critical_missing(hypothesis_id))

        components = {
            "llm_reasoning_prior": input_.llm_reasoning_prior,
            "evidence_support": evidence_support,
            "source_reliability": source_reliability,
            "temporal_relation": temporal_relation,
        }

        base_score = (
            input_.llm_reasoning_prior * WEIGHT_LLM_PRIOR
            + evidence_support * WEIGHT_EVIDENCE_SUPPORT
            + source_reliability * WEIGHT_SOURCE_RELIABILITY
            + temporal_relation * WEIGHT_TEMPORAL
        )

        penalties = self._compute_penalties(critical_count, normal_count, missing_critical_count)
        penalty_sum = sum(p.value for p in penalties)
        final_score = _clamp(base_score - penalty_sum, 0.0, 1.0)

        return ScoreBreakdown(
            hypothesis_id=hypothesis_id,
            base_score=base_score,
            components=components,
            penalties=penalties,
            final_score=final_score,
        )

    def _compute_penalties(self, critical: int, normal: int, missing: int) -> List[Penalty]:
        penalties: List[Penalty] = []
        crit_val = _cap(critical, PENALTY_CRITICAL, PENALTY_CRITICAL_CAP)
        if crit_val > 0:
            penalties.append(Penalty("critical_contradiction", crit_val,
                                     f"{critical} critical contradiction(s)"))
        norm_val = _cap(normal, PENALTY_NORMAL, PENALTY_NORMAL_CAP)
        if norm_val > 0:
            penalties.append(Penalty("normal_contradiction", norm_val,
                                     f"{normal} normal contradiction(s)"))
        miss_val = _cap(missing, PENALTY_MISSING_CRITICAL, PENALTY_MISSING_CAP)
        if miss_val > 0:
            penalties.append(Penalty("missing_critical", miss_val,
                                     f"{missing} missing critical evidence"))
        return penalties


def _unique_reliability_mean(support_matcher: Any, hypothesis_id: str) -> float:
    """§三十八 Source Reliability component = unique supporting evidence reliability 均值。"""
    rels = support_matcher.relations_for(hypothesis_id)
    if not rels:
        return 0.0
    return sum(r.source_reliability for r in rels) / len(rels)


def _contradiction_counts(checker: Any, hypothesis_id: str):
    """返回 (critical_unresolved, normal_unresolved)。"""
    critical = 0
    normal = 0
    for c in checker.all_contradictions(hypothesis_id):
        if c.resolved:
            continue
        if c.severity == "critical":
            critical += 1
        else:
            normal += 1
    return critical, normal


def _compute_temporal(input_: RcaEvaluationInput) -> float:
    """§三十九 temporal_relation：从 timeline 内部计算；无 timeline 默认弱（0.40）。"""
    tl = input_.timeline
    if tl is None:
        return TEMPORAL_WEAK
    hid = input_.hypothesis.hypothesis_id
    # hypothesis 因果事件 ref（由 RcaEngine 在构建 timeline 时以 evidence/事件关联）
    cause_event = getattr(input_.hypothesis, "affected_event_id", "")
    if not cause_event or not input_.abnormal_event_id:
        return TEMPORAL_WEAK
    return tl.temporal_relation(cause_event, input_.abnormal_event_id)


def _cap(count: int, per: float, cap: float) -> float:
    return min(count * per, cap)


def _clamp(v: float, lo: float, hi: float) -> float:
    return max(lo, min(hi, v))
