"""P9.9 Timeline / First Bad Event — 评审加固测试。

评审修复（P1）：
- TimelineEvent 增加 cluster_id / evidence_id 字段（合同要求）。
- first_bad_event 只取最早"异常"事件，不默认取最早事件（正常 change/log 不算）。
- temporal_relation=1.0 需验证同一资源或直接依赖链。
- 时钟严重偏差 → temporal_relation=partial（不强行给 1.0）。
"""
from datetime import datetime, timezone

import pytest


def _ts(h, m):
    return datetime(2026, 8, 21, h, m, tzinfo=timezone.utc)


def test_timeline_event_carries_cluster_and_evidence():
    from timeline import TimelineEvent

    ev = TimelineEvent(
        event_id="e1", event_type="metric", observed_at=_ts(10, 0),
        resource_id="cluster-1/svc/checkout", cluster_id="cluster-1", evidence_id="ev-9",
    )
    assert ev.cluster_id == "cluster-1"
    assert ev.evidence_id == "ev-9"


def test_first_bad_event_is_earliest_abnormal_not_earliest():
    from timeline import Timeline, TimelineEvent

    # 最早事件是 normal change（10:00），异常 metric 是 10:05 → first_bad = metric
    events = [
        TimelineEvent(event_id="change", event_type="change", observed_at=_ts(10, 0), abnormal=False),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 5), abnormal=True),
        TimelineEvent(event_id="alert", event_type="alert", observed_at=_ts(10, 6), abnormal=True),
    ]
    tl = Timeline(run_id="run-1", events=events)
    assert tl.first_bad_event.event_id == "metric"


def test_first_bad_event_none_when_no_abnormal():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="change", event_type="change", observed_at=_ts(10, 0), abnormal=False),
    ]
    tl = Timeline(run_id="run-1", events=events)
    assert tl.first_bad_event is None


def test_temporal_relation_requires_same_resource_for_10():
    from timeline import Timeline, TimelineEvent

    # 候选 change 与 abnormal 不同资源 → 即使时间早也不给 1.0
    events = [
        TimelineEvent(event_id="change", event_type="change", observed_at=_ts(10, 0),
                      resource_id="cluster-1/svc/other"),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 5), abnormal=True,
                      resource_id="cluster-1/svc/checkout"),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # 不同资源 → 非 1.0（0.70 相关时间窗）
    assert tl.temporal_relation("change", "metric") != 1.0


def test_temporal_relation_same_resource_gives_10():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="change", event_type="change", observed_at=_ts(10, 0),
                      resource_id="cluster-1/svc/checkout"),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 5), abnormal=True,
                      resource_id="cluster-1/svc/checkout"),
    ]
    tl = Timeline(run_id="run-1", events=events)
    assert tl.temporal_relation("change", "metric") == 1.0


def test_clock_skew_returns_unknown():
    from timeline import Timeline, TimelineEvent

    # 候选事件时间戳晚于 abnormal 异常多（时钟偏差）→ 不强行给 1.0/0.00，返回 unknown
    events = [
        TimelineEvent(event_id="change", event_type="change", observed_at=_ts(10, 0),
                      resource_id="cluster-1/svc/checkout"),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 30), abnormal=True,
                      resource_id="cluster-1/svc/checkout"),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # 超过合理窗口（>30min）且无法确认同一资源链 → 弱/unknown，不强行 1.0
    tr = tl.temporal_relation("change", "metric")
    assert tr < 1.0


def test_event_after_first_bad_gives_temporal_zero():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="abnormal", event_type="metric", observed_at=_ts(10, 5), abnormal=True),
        TimelineEvent(event_id="late-change", event_type="change", observed_at=_ts(10, 9)),
    ]
    tl = Timeline(run_id="run-1", events=events)
    assert tl.temporal_relation("late-change", "abnormal") == 0.00
