"""P9.4 Contradiction Checker — V9.3 Phase9。

主动搜索反证：时间矛盾、资源/cluster 矛盾、指标与日志/trace 矛盾、变更发生在故障后等反证（§七十五 P9.4）。
severity: critical | normal。
铁律：unresolved critical contradiction → 不得 confirmed，无论 score 多高（§四十）。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, List

CONTRADICTION_TYPES = {
    "time_conflict",
    "resource_cluster_conflict",
    "metric_log_trace_conflict",
    "change_after_fault",
    "temporal_relation_weak",
}

SEVERITIES = {"critical", "normal"}


class ContradictionError(ValueError):
    def __init__(self, message: str):
        self.error_code = "CONTRADICTION_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class Contradiction:
    """一条反证。"""

    contradiction_id: str
    hypothesis_id: str
    evidence_id: str
    contradiction_type: str
    severity: str
    resolved: bool = False
    description: str = ""


class ContradictionChecker:
    """内存 Contradiction Checker：记录反证，判定是否阻断 confirmed。"""

    def __init__(self) -> None:
        self._contradictions: Dict[str, List[Contradiction]] = {}
        self._seq = 0

    def add_contradiction(
        self,
        hypothesis_id: str,
        evidence_id: str,
        contradiction_type: str,
        severity: str,
        description: str = "",
    ) -> Contradiction:
        if contradiction_type not in CONTRADICTION_TYPES:
            raise ContradictionError(f"非法 contradiction_type: {contradiction_type}")
        if severity not in SEVERITIES:
            raise ContradictionError(f"非法 severity: {severity}")
        self._seq += 1
        c = Contradiction(
            contradiction_id=f"ct-{self._seq}",
            hypothesis_id=hypothesis_id,
            evidence_id=evidence_id,
            contradiction_type=contradiction_type,
            severity=severity,
            resolved=False,
            description=description,
        )
        self._contradictions.setdefault(hypothesis_id, []).append(c)
        return c

    def detect(
        self,
        hypothesis: Any,
        evidences: List[Any],
        *,
        timeline: Any = None,
        abnormal_event_id: str = "",
    ) -> List[Contradiction]:
        """从 Evidence/Hypothesis/Timeline 主动推导反证（§七十五 P9.4，非手工容器）。

        推导类别：
        - resource_cluster_conflict: Evidence.cluster_id ≠ Hypothesis.cluster_id（跨 cluster）
        - change_after_fault: 通过 Timeline，change 事件发生在 First Bad Event 之后
        - metric_log_trace_conflict: 同资源 metric 与 log/trace 事实语义矛盾（候选）
        - temporal_relation_weak: hypothesis.potential_contradiction 声明的时间关系弱
        """
        hid = getattr(hypothesis, "hypothesis_id", None) or hypothesis.hypothesis_id
        h_cluster = getattr(hypothesis, "cluster_id", "")
        detected: List[Contradiction] = []

        # 1. cross cluster
        for ev in evidences:
            ev_cluster = getattr(ev, "cluster_id", "")
            if ev_cluster and h_cluster and ev_cluster != h_cluster:
                detected.append(self.add_contradiction(
                    hid, ev.evidence_id, "resource_cluster_conflict", "critical",
                    f"Evidence {ev.evidence_id} 跨 cluster: {ev_cluster} != {h_cluster}",
                ))

        # 2. change after fault（经 timeline）
        if timeline is not None:
            for ev in evidences:
                etype = getattr(ev, "evidence_type", "")
                if etype != "change":
                    continue
                # 用 timeline 判断 change 相对 abnormal 是否在之后
                ev_id = ev.evidence_id
                by_id = {e.event_id: e for e in timeline.events}
                if ev_id in by_id and abnormal_event_id in by_id:
                    if by_id[ev_id].observed_at >= by_id[abnormal_event_id].observed_at:
                        detected.append(self.add_contradiction(
                            hid, ev_id, "change_after_fault", "critical",
                            f"change {ev_id} 发生在异常 {abnormal_event_id} 之后",
                        ))

        # 3. metric vs log/trace 矛盾（候选）
        for ev in evidences:
            etype = getattr(ev, "evidence_type", "")
            if etype == "metric_anomaly":
                fact = getattr(ev, "fact", "")
                for other in evidences:
                    otype = getattr(other, "evidence_type", "")
                    if otype in ("log_error", "trace_anomaly"):
                        # metric 声称正常但 log/trace 异常 → 数据源间矛盾
                        if fact and "normal" in fact.lower():
                            detected.append(self.add_contradiction(
                                hid, other.evidence_id, "metric_log_trace_conflict", "normal",
                                f"metric 正常但 {otype} 异常（同资源数据源矛盾）",
                            ))

        # 4. temporal_relation_weak（hypothesis 声明）
        for ctype in getattr(hypothesis, "potential_contradiction", []) or []:
            if ctype == "temporal_relation_weak":
                detected.append(self.add_contradiction(
                    hid, "", "temporal_relation_weak", "normal",
                    "hypothesis 声明时间关系弱",
                ))

        return detected

    def resolve(self, hypothesis_id: str, evidence_id: str) -> None:
        """解决某条反证（按 evidence_id 匹配）。"""
        rels = self._contradictions.get(hypothesis_id, [])
        for i, c in enumerate(rels):
            if c.evidence_id == evidence_id:
                self._contradictions[hypothesis_id][i] = _resolved(c)

    def unresolved_critical(self, hypothesis_id: str) -> List[Contradiction]:
        return [
            c for c in self._contradictions.get(hypothesis_id, [])
            if c.severity == "critical" and not c.resolved
        ]

    def all_contradictions(self, hypothesis_id: str) -> List[Contradiction]:
        return list(self._contradictions.get(hypothesis_id, []))

    def has_unresolved_critical(self, hypothesis_id: str) -> bool:
        return bool(self.unresolved_critical(hypothesis_id))

    def blocks_confirmation(self, hypothesis_id: str) -> bool:
        """存在 unresolved critical contradiction → 阻断 confirmed（§四十铁律）。"""
        return self.has_unresolved_critical(hypothesis_id)


def _resolved(c: Contradiction) -> Contradiction:
    from dataclasses import replace

    return replace(c, resolved=True)
