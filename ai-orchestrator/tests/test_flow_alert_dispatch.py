# tests/test_flow_alert_dispatch.py
from flow_engine.flow_alert_dispatch import dispatch_alert


def test_dispatch_matches_alert_fired_only():
    flows = [
        {"id": "f1", "graph": {"nodes": [{"type": "trigger.alert_fired",
                                          "config": {"rule": "high-cpu", "min_severity": "warning"}}]}},
        {"id": "f2", "graph": {"nodes": [{"type": "trigger.cron", "config": {}}]}},
    ]
    ran = []

    def run_flow(fid, trigger):
        ran.append((fid, trigger))
        return f"run_{fid}"

    fired = dispatch_alert(lambda: flows, run_flow, "high-cpu", "critical", {"alert": 1})
    assert fired == [{"flow_id": "f1", "run_id": "run_f1"}]
    assert len(ran) == 1 and ran[0][0] == "f1"          # 只触发命中者
    assert ran[0][1]["type"] == "alert_fired"            # trigger 类型透传
    assert ran[0][1]["payload"] == {"alert": 1}          # payload 透传


def test_dispatch_rule_not_matching():
    flows = [{"id": "f1", "graph": {"nodes": [{"type": "trigger.alert_fired",
                                               "config": {"rule": "high-cpu"}}]}}]
    ran = []
    fired = dispatch_alert(lambda: flows, lambda fid, trigger: ran.append(fid) or "r",
                           "high-mem", "critical", {})
    assert fired == [] and ran == []


def test_dispatch_severity_below_minimum():
    flows = [{"id": "f1", "graph": {"nodes": [{"type": "trigger.alert_fired",
                                               "config": {"rule": "", "min_severity": "critical"}}]}}]
    ran = []
    fired = dispatch_alert(lambda: flows, lambda fid, trigger: ran.append(fid) or "r",
                           "high-cpu", "warning", {})
    assert fired == [] and ran == []
