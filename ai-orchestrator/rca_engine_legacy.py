"""RcaEngine — V9.3 Phase 9 单一 RCA 编排器（评审加固版）。

评审修复（P0）：新 RCA 必须接入生产主链，不再是孤立模块。
RcaEngine 是 Phase 9 正式 RCA 编排器，输入只能是 Evidence Hub 生成的 Evidence，
输出 RootCause/Confidence/Unknowns。调用方不能绕过 Evidence/推导器直接判根因。

统一链：
  Evidence（Evidence Hub）→ RcaInputSnapshot
    → HypothesisGenerator.generate
    → per-hypothesis: SupportMatcher + ContradictionChecker.detect + MissingEvidenceEngine.derive
    → ScoringEngine.score(RcaEvaluationInput)   // 分量内部计算
    → RootCauseRanker.rank
    → Timeline / First Bad Event（时间推断）
    → UnknownSafeHandler（root_cause=unknown / explicit missing / no auto remediation）

冻结 Source Reliability（§三十六，DeepSeek 不得调高）：
  用于把 Evidence.source/evidence_type 映射到固定 reliability。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, List, Optional

import contracts
from contracts_identity import rca_id
from rca_snapshot import RcaInputSnapshot
from hypothesis import HypothesisGenerator
from support_matcher import SupportMatcher
from contradiction_checker import ContradictionChecker
from missing_evidence import MissingEvidenceEngine
from scoring_engine import RcaEvaluationInput, ScoringEngine
from root_cause_ranker import RootCauseRanker
from timeline import Timeline, TimelineEvent
from unknown_handler import UnknownSafeHandler

# §三十六 Source Reliability V1 固定表（冻结，不得调高）
SOURCE_RELIABILITY_V1 = {
    "metric_anomaly": 0.95,     # Metric / SLI
    "k8s_state": 0.95,          # Kubernetes API current state
    "trace_anomaly": 0.90,      # Trace / Span
    "k8s_event": 0.90,          # Kubernetes Event
    "change": 0.90,             # Structured Change Record
    "topology_relation": 0.85,  # Resource Graph deterministic
    "resource_state": 0.85,     # Resource Graph deterministic
    "log_error": 0.85,          # Raw Log
    "log_pattern": 0.80,        # Log Pattern
    "capacity_anomaly": 0.80,
    "hardware_event": 0.85,     # DeepFlow observation
    "alert": 0.85,
    "knowledge_case": 0.60,     # Historical Case
}
# fallback（未映射的 Evidence 类型）
SOURCE_RELIABILITY_FALLBACK = 0.60


class EvidenceScopeMismatch(Exception):
    """R2 收敛 v0.3 §8：Evidence 隔离维度（run/tenant/cluster）与 Engine 不一致 → fail-closed。"""


@dataclass
class HypothesisEvaluation:
    """内部计算单元（非 wire）：保存单 hypothesis 的完整推导轨迹，供无损投影。"""
    hypothesis: Any
    score_breakdown: Any
    support_relations: List[Any]
    contradictions: List[Any]
    missing_evidence: List[Any]
    support_matcher: SupportMatcher


@dataclass
class RcaComputation:
    """内部计算轨迹（非 wire，禁止外传；仅内部测试/投影）。"""
    snapshot: Any
    evaluations: dict  # hypothesis_id(UUID) → HypothesisEvaluation
    ranking: Any
    conclusion_state: str
    timeline: Any


class RcaEngine:
    """Phase 9 正式 RCA 编排器：Evidence → Root Cause/Unknown。"""

    def __init__(
        self,
        *,
        tenant_id: str,
        cluster_id: str,
        llm_reasoning_prior_default: float = 0.0,
    ) -> None:
        self.tenant_id = tenant_id
        self.cluster_id = cluster_id
        self.llm_reasoning_prior_default = llm_reasoning_prior_default
        self.scorer = ScoringEngine()
        self.ranker = RootCauseRanker()
        self.unknown_handler = UnknownSafeHandler()

    def _validate_evidence_scope(
        self,
        run_id: str,
        evidences: List[Any],
    ) -> None:
        """R2 收敛 v0.3 §8：snapshot 前逐条校验 Evidence 隔离维度，不一致 fail-closed。

        审计：跨 run/tenant/cluster 的 Evidence 必须在评分前被拒绝（而非仅输出 contradiction）。
        """
        for ev in evidences:
            ev_run = str(getattr(ev, "run_id", "") or "")
            ev_tenant = str(getattr(ev, "tenant_id", "") or "")
            ev_cluster = str(getattr(ev, "cluster_id", "") or "")
            if ev_run and ev_run != run_id:
                raise EvidenceScopeMismatch(
                    f"Evidence {ev.evidence_id} run_id={ev_run} 与 Engine run_id={run_id} 不一致"
                )
            if ev_tenant and ev_tenant != self.tenant_id:
                raise EvidenceScopeMismatch(
                    f"Evidence {ev.evidence_id} tenant_id={ev_tenant} 与 Engine tenant_id={self.tenant_id} 不一致"
                )
            if ev_cluster and ev_cluster != self.cluster_id:
                raise EvidenceScopeMismatch(
                    f"Evidence {ev.evidence_id} cluster_id={ev_cluster} 与 Engine cluster_id={self.cluster_id} 不一致"
                )

    def evaluate(
        self,
        *,
        run_id: str,
        intent_id: str,
        resource_id: str,
        symptoms: List[str],
        evidences: List[Any],
        llm_reasoning_prior: Optional[float] = None,
        snapshot_version: str = "v1",
    ) -> RcaComputation:
        """内部计算：Evidence → RcaComputation（含 evaluations 索引，供无损投影）。"""
        llm_prior = (
            llm_reasoning_prior
            if llm_reasoning_prior is not None
            else self.llm_reasoning_prior_default
        )

        # 0. Evidence 预校验 fail-closed（跨 run/tenant/cluster 拒绝）
        self._validate_evidence_scope(run_id, evidences)

        # 1. 冻结 Evidence snapshot（强隔离：tenant/cluster）
        snap = RcaInputSnapshot(
            run_id=run_id,
            intent_id=intent_id,
            evidence_ids=tuple(ev.evidence_id for ev in evidences),
            snapshot_version=snapshot_version,
            tenant_id=self.tenant_id,
            cluster_id=self.cluster_id,
        )

        # 2. Timeline（从 Evidence 构建，供 temporal_relation + FirstBadEvent）
        timeline = self._build_timeline(evidences)

        # 3. Hypothesis 生成（多 candidate）
        gen = HypothesisGenerator()
        hypotheses = gen.generate(
            run_id, symptoms,
            tenant_id=self.tenant_id, cluster_id=self.cluster_id, resource_id=resource_id,
        )

        # 4. per-hypothesis 推导 + 评分（保存 evaluations 索引）
        ranked = []
        evaluations: dict = {}
        for h in hypotheses:
            sm = SupportMatcher()
            cc = ContradictionChecker()
            me = MissingEvidenceEngine()
            self._populate_support(sm, h, evidences)
            cc.detect(h, evidences, timeline=timeline, abnormal_event_id=self._first_abnormal_id(timeline))
            me.derive(h, evidences)
            eval_input = RcaEvaluationInput(
                snapshot=snap,
                hypothesis=h,
                support_matcher=sm,
                contradiction_checker=cc,
                missing_engine=me,
                timeline=timeline,
                llm_reasoning_prior=llm_prior,
                abnormal_event_id=self._first_abnormal_id(timeline),
            )
            breakdown = self.scorer.score(eval_input)
            direct_rel = _max_direct_reliability(sm, h.hypothesis_id)
            h_contradictions = cc.all_contradictions(h.hypothesis_id)
            h_missing = me.all_missing(h.hypothesis_id)
            ranked.append(
                _Rankable(
                    hypothesis_id=h.hypothesis_id,
                    claim=h.claim,
                    final_score=breakdown.final_score,
                    direct_evidence_reliability=direct_rel,
                    has_unresolved_critical=cc.has_unresolved_critical(h.hypothesis_id),
                    has_critical_missing=me.has_critical_missing(h.hypothesis_id),
                    contradictions=h_contradictions,
                    missing=h_missing,
                )
            )
            evaluations[str(h.hypothesis_id)] = HypothesisEvaluation(
                hypothesis=h,
                score_breakdown=breakdown,
                support_relations=sm.relations_for(h.hypothesis_id),
                contradictions=h_contradictions,
                missing_evidence=h_missing,
                support_matcher=sm,
            )

        ranking = self.ranker.rank(ranked, run_id=run_id)
        return RcaComputation(
            snapshot=snap,
            evaluations=evaluations,
            ranking=ranking,
            conclusion_state=ranking.confidence_state,
            timeline=timeline,
        )

    def run(
        self,
        *,
        run_id: str,
        intent_id: str,
        resource_id: str,
        symptoms: List[str],
        evidences: List[Any],
        llm_reasoning_prior: Optional[float] = None,
        snapshot_version: str = "v1",
    ) -> "contracts.RcaResult":
        """唯一公开结果：RcaComputation → contracts.RcaResult（无损、可复算投影）。"""
        comp = self.evaluate(
            run_id=run_id, intent_id=intent_id, resource_id=resource_id,
            symptoms=symptoms, evidences=evidences,
            llm_reasoning_prior=llm_reasoning_prior, snapshot_version=snapshot_version,
        )
        ranking = comp.ranking
        missing_all = sorted(
            {m.required_type for e in comp.evaluations.values() for m in e.missing_evidence}
        )

        # Unknown-safe（root_cause=None → "unknown" 由 handle 处理）
        root_hid = ranking.root_cause.hypothesis_id if ranking.root_cause else None
        unknown = self.unknown_handler.handle(
            run_id=run_id, root_cause=root_hid, missing_evidence=missing_all,
        )

        # 投影：hypothesis_scores / contradictions / missing（无损关联）
        hypothesis_scores = [
            contracts.HypothesisScore(
                llm_prior=e.score_breakdown.components["llm_reasoning_prior"],
                evidence_support=e.score_breakdown.components["evidence_support"],
                source_reliability=e.score_breakdown.components["source_reliability"],
                temporal=e.score_breakdown.components["temporal_relation"],
                contradiction_penalty=_penalty_sum(
                    e.score_breakdown, {"critical_contradiction", "normal_contradiction"}),
                missing_penalty=_penalty_sum(e.score_breakdown, {"missing_critical"}),
                final_score=e.score_breakdown.final_score,
                hypothesis_id=_to_uuid(e.hypothesis.hypothesis_id),
            )
            for e in comp.evaluations.values()
        ]
        contradictions_proj = [
            contracts.Contradiction(
                kind=c.contradiction_type, severity=c.severity,
                detail=c.description, resolved=c.resolved,
                hypothesis_id=_to_uuid(c.hypothesis_id),
                evidence_id=_to_uuid(c.evidence_id) if c.evidence_id else None,
            )
            for e in comp.evaluations.values() for c in e.contradictions
        ]
        missing_proj = [
            contracts.MissingEvidence(
                kind="critical" if m.critical else "optional",
                reason=m.reason,
                description=m.required_type,
                hypothesis_id=_to_uuid(m.hypothesis_id),
                required_type=m.required_type,
                followup_slot=m.followup_slot,
            )
            for e in comp.evaluations.values() for m in e.missing_evidence
        ]

        # root_cause_refs（仅被选中 hypothesis，用 evaluations 索引 —— 评审 §sm 错误索引）
        root_cause_refs = []
        if ranking.root_cause is not None:
            selected = comp.evaluations[str(ranking.root_cause.hypothesis_id)]
            root_cause_refs.append(contracts.RootCauseRef(
                hypothesis_id=_to_uuid(ranking.root_cause.hypothesis_id),
                evidence_ids=[_to_uuid(r.evidence_id) for r in selected.support_relations],
                final_score=ranking.root_cause.final_score,
            ))

        # root_cause = claim（评审：UUID 只在 RootCauseRef，权威 root_cause 用描述）
        root_cause = None
        if ranking.root_cause is not None:
            selected = comp.evaluations[str(ranking.root_cause.hypothesis_id)]
            root_cause = getattr(selected.hypothesis, "claim", None) or None

        conclusion_state = ranking.confidence_state
        confidence = _root_cause_confidence(ranking)
        status = "completed" if conclusion_state in ("confirmed", "supported") else "unknown"

        return contracts.RcaResult(
            rca_id=rca_id(run_id, resource_id, snapshot_version),
            run_id=_to_uuid(run_id),
            tenant_id=_to_uuid(self.tenant_id),
            cluster_id=_to_uuid(self.cluster_id),
            resource_id=resource_id,
            root_cause=root_cause,
            confidence=confidence,
            status=status,
            conclusion_state=conclusion_state,
            hypothesis_scores=hypothesis_scores,
            contradictions=contradictions_proj,
            missing_evidence=missing_proj,
            root_cause_refs=root_cause_refs,
            automatic_remediation=unknown.automatic_remediation,
            ops_actions=list(unknown.ops_actions),
        )

    def _populate_support(self, sm: SupportMatcher, h: Any, evidences: List[Any]) -> None:
        """从 Evidence 构建支持关系：同资源/同 cluster 的 Evidence → direct_support。"""
        for ev in evidences:
            ev_cluster = getattr(ev, "cluster_id", "")
            if ev_cluster and ev_cluster != self.cluster_id:
                continue  # 跨 cluster 不建立支持（由 contradiction 处理）
            # reliability 从冻结表映射（§三十六）
            rel = SOURCE_RELIABILITY_V1.get(getattr(ev, "evidence_type", ""), SOURCE_RELIABILITY_FALLBACK)
            sm.add_relation(h.hypothesis_id, ev.evidence_id, rel, "direct_support")

    def _build_timeline(self, evidences: List[Any]) -> Optional[Timeline]:
        if not evidences:
            return None
        events = []
        for ev in evidences:
            observed = getattr(ev, "observed_at", None)
            if observed is None:
                continue
            etype = getattr(ev, "evidence_type", "")
            events.append(TimelineEvent(
                event_id=ev.evidence_id,
                event_type=_map_timeline_type(etype),
                observed_at=observed,
                resource_id=getattr(ev, "resource_id", "") or "",
                cluster_id=getattr(ev, "cluster_id", "") or "",
                evidence_id=ev.evidence_id,
                abnormal=(etype in {"metric_anomaly", "alert", "log_error", "trace_anomaly", "k8s_event"}),
            ))
        if not events:
            return None
        return Timeline(run_id=evidences[0].run_id, events=events)

    @staticmethod
    def _first_abnormal_id(timeline: Optional[Timeline]) -> str:
        if timeline is None or timeline.first_bad_event is None:
            return ""
        return timeline.first_bad_event.event_id


class _Rankable:
    """内部可排序对象（RootCauseRanker 消费）。"""

    def __init__(self, hypothesis_id, claim, final_score, direct_evidence_reliability,
                 has_unresolved_critical, has_critical_missing,
                 contradictions=None, missing=None):
        self.hypothesis_id = hypothesis_id
        self.claim = claim
        self.final_score = final_score
        self.direct_evidence_reliability = direct_evidence_reliability
        self.has_unresolved_critical = has_unresolved_critical
        self.has_critical_missing = has_critical_missing
        # 审计修复：回传推导结果，避免 contradictions/missing 被丢弃。
        self.contradictions = list(contradictions or [])
        self.missing = list(missing or [])


def _max_direct_reliability(sm: SupportMatcher, hypothesis_id: str) -> float:
    rels = sm.relations_for(hypothesis_id)
    if not rels:
        return 0.0
    return max(r.source_reliability for r in rels)


def _penalty_sum(breakdown: Any, types: set) -> float:
    """penalty 负值映射（R2 收敛 v0.3 §4.1）。

    contracts.HypothesisScore.contradiction_penalty/missing_penalty 要求 <=0（Field(le=0)），
    而 ScoreBreakdown.Penalty.value 是正数 → 必须取负，否则权威模型 ValidationError。
    """
    return -round(sum(p.value for p in breakdown.penalties if p.type in types), 4)


def _root_cause_confidence(ranking: Any) -> float:
    """权威 confidence = 被选中 root_cause hypothesis 的真实 final_score；unknown 时 0.0。

    决策（v0.3 §6）：confidence 必须反映 ScoringEngine 真实可复现评分，不用枚举映射。
    """
    if ranking.root_cause is None:
        return 0.0
    return round(ranking.root_cause.final_score, 4)


def _to_uuid(value: Any) -> Any:
    """尽力转 UUID；非 UUID 保留原值（供 _to_uuid 在隔离测试/非规范标签时稳健）。"""
    from uuid import UUID as _UUID

    if value is None or str(value).strip() == "":
        return None
    try:
        return _UUID(str(value))
    except (ValueError, TypeError):
        return str(value).strip()


def _map_timeline_type(evidence_type: str) -> str:
    """Evidence type → Timeline event type 映射。"""
    mapping = {
        "metric_anomaly": "metric",
        "log_pattern": "log_pattern",
        "log_error": "log_pattern",
        "trace_anomaly": "trace",
        "k8s_state": "event",
        "k8s_event": "event",
        "alert": "alert",
        "change": "change",
        "knowledge_case": "event",
        "topology_relation": "event",
        "resource_state": "event",
        "capacity_anomaly": "metric",
        "hardware_event": "event",
    }
    return mapping.get(evidence_type, "event")
