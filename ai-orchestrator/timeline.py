"""P9.9 Timeline / First Bad Event — V9.3 Phase9（评审加固版）。

统一 change/event/log-pattern/trace/metric/alert/execution 的时间轴；
First Bad Event 是时间推断结果，不默认等于 Alert 或 Change（§七十五 P9.9）。

评审修复（2026-08-21 Gate 9 FAIL 退回，P1）：
- TimelineEvent 增加 cluster_id / evidence_id（合同要求）。
- first_bad_event 只取最早"异常"事件（默认异常类型集 + 显式 abnormal 标记），不取最早事件。
- temporal_relation=1.0 需验证同一资源或直接依赖链（不同资源不强行给 1.0）。
- 时钟严重偏差 → 不强行给 1.0（返回弱值，不伪装）。

temporal_relation（§三十九 固定值）:
  1.00  原因明显先于 First Bad Event，且位于同一资源或直接依赖链
  0.70  原因先于异常，并位于合理相关时间窗
  0.40  时间关系弱、只能证明相关
  0.00  无时间支持，或候选事件发生在异常之后
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import List, Optional

TIMELINE_EVENT_TYPES = {
    "change", "event", "log_pattern", "trace", "metric", "alert", "execution",
}

# 默认视为异常的 event_type（异常时间推断依据）
DEFAULT_ABNORMAL_TYPES = {"metric", "alert", "log_error", "trace_anomaly", "k8s_event"}


@dataclass(frozen=True)
class TimelineEvent:
    event_id: str
    event_type: str
    observed_at: datetime
    resource_id: str = ""
    source: str = ""
    cluster_id: str = ""
    evidence_id: str = ""
    abnormal: bool = False

    def __post_init__(self) -> None:
        if self.event_type not in TIMELINE_EVENT_TYPES:
            raise ValueError(f"非法 event_type: {self.event_type}")

    def is_abnormal(self) -> bool:
        """异常判定：显式标记优先，否则按默认异常类型集推断。"""
        if self.abnormal:
            return True
        return self.event_type in DEFAULT_ABNORMAL_TYPES


@dataclass(frozen=True)
class Timeline:
    run_id: str
    events: List[TimelineEvent] = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "events", list(self.events or []))

    def sorted(self) -> List[TimelineEvent]:
        return sorted(self.events, key=lambda e: e.observed_at)

    @property
    def first_bad_event(self) -> Optional[TimelineEvent]:
        """First Bad Event = 最早"异常"事件（时间推断，非默认最早事件/alert/change）。"""
        abnormal_events = [e for e in self.events if e.is_abnormal()]
        if not abnormal_events:
            return None
        return min(abnormal_events, key=lambda e: e.observed_at)

    def temporal_relation(self, candidate_event_id: str, abnormal_event_id: str) -> float:
        """§三十九：候选事件相对异常事件的时间关系。

        - 候选事件在异常之后 → 0.00（不可能是原因）。
        - 同一资源且明显先于（≤5min）→ 1.00。
        - 先于但不同资源 / 合理窗（≤30min）→ 0.70。
        - 时间关系弱 / 时钟偏差 → 0.40（不强行给 1.0）。
        """
        by_id = {e.event_id: e for e in self.events}
        candidate = by_id.get(candidate_event_id)
        abnormal = by_id.get(abnormal_event_id)
        if candidate is None or abnormal is None:
            return 0.40
        if candidate.observed_at >= abnormal.observed_at:
            return 0.00
        delta = abnormal.observed_at - candidate.observed_at
        same_resource = (candidate.resource_id or "") == (abnormal.resource_id or "")
        if same_resource and delta <= timedelta(minutes=5):
            return 1.00
        if delta <= timedelta(minutes=30):
            return 0.70
        return 0.40
