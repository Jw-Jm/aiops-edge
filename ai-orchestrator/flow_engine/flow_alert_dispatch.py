# flow_engine/flow_alert_dispatch.py
"""告警触发: 告警事件匹配 trigger.alert_fired 节点并运行 workflow"""
import logging

from flow_engine.nodes_trigger import alert_matches
from flow_engine.trigger_scheduler import _now_utc

log = logging.getLogger(__name__)


def dispatch_alert(list_enabled_flows, run_flow, rule: str, severity: str, payload: dict) -> list:
    fired = []
    for f in list_enabled_flows():
        for node in (f.get("graph") or {}).get("nodes", []):
            if node.get("type") != "trigger.alert_fired":
                continue
            if not alert_matches(node.get("config") or {}, rule, severity):
                continue
            run_id = run_flow(f["id"], trigger={
                "type": "alert_fired", "fired_at": _now_utc(),
                "payload": payload or {}})
            fired.append({"flow_id": f["id"], "run_id": run_id})
            break  # 每个 flow 最多触发一次
    return fired
