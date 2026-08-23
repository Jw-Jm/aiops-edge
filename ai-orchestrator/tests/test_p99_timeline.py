"""P9.9 Timeline / First Bad Event — V9.3 Phase9（TDD RED 测试）。

统一 change/event/log-pattern/trace/metric/alert/execution 的时间轴；
First Bad Event 是时间推断结果，不默认等于 Alert 或 Change（§七十五 P9.9）。
事件发生在 First Bad Event 后 → temporal_relation=0.00（§三十九）。
"""
from datetime import datetime, timezone


def _ts(h, m):
    return datetime(2026, 8, 21, h, m, tzinfo=timezone.utc)


def test_timeline_unifies_multiple_event_types():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="e1", event_type="change", observed_at=_ts(10, 0)),
        TimelineEvent(event_id="e2", event_type="metric", observed_at=_ts(10, 5)),
        TimelineEvent(event_id="e3", event_type="alert", observed_at=_ts(10, 3)),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # 按 observed_at 升序
    assert [e.event_id for e in tl.sorted()] == ["e1", "e3", "e2"]


def test_first_bad_event_is_inferred_not_alert_or_change():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="change-later", event_type="change", observed_at=_ts(10, 10)),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 1)),
        TimelineEvent(event_id="alert", event_type="alert", observed_at=_ts(10, 2)),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # First Bad Event 是最早的 abnormal（metric 10:01），不是 alert/change
    assert tl.first_bad_event.event_id == "metric"


def test_first_bad_event_not_default_alert():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="alert", event_type="alert", observed_at=_ts(10, 2)),
        TimelineEvent(event_id="metric", event_type="metric", observed_at=_ts(10, 1)),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # metric 更早 → first_bad_event 是 metric，不是 alert
    assert tl.first_bad_event.event_id == "metric"


def test_event_after_first_bad_gives_temporal_zero():
    from timeline import Timeline, TimelineEvent

    events = [
        TimelineEvent(event_id="cause", event_type="change", observed_at=_ts(10, 0)),
        TimelineEvent(event_id="abnormal", event_type="metric", observed_at=_ts(10, 5)),
    ]
    tl = Timeline(run_id="run-1", events=events)
    # change 在 abnormal 之前 → temporal_relation=1.0（原因先于异常）
    assert tl.temporal_relation("cause", "abnormal") == 1.0
    # abnormal 之后的事件 → 0.00（不可能是原因）
    events2 = [
        TimelineEvent(event_id="abnormal", event_type="metric", observed_at=_ts(10, 5)),
        TimelineEvent(event_id="late-change", event_type="change", observed_at=_ts(10, 9)),
    ]
    tl2 = Timeline(run_id="run-1", events=events2)
    assert tl2.temporal_relation("late-change", "abnormal") == 0.00


def test_empty_timeline_no_first_bad_event():
    from timeline import Timeline

    tl = Timeline(run_id="run-1", events=[])
    assert tl.first_bad_event is None
