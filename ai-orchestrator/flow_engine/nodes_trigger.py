# flow_engine/nodes_trigger.py
"""触发器节点: trigger.manual / trigger.cron / trigger.alert_fired"""
from flow_engine.expr import RunContext

TRIGGER_NODES = {
    "trigger.manual": {
        "label": "手动触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {"description": {"type": "text", "label": "说明", "default": ""}},
    },
    "trigger.cron": {
        "label": "定时触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {"cron": {"type": "text", "label": "Cron 表达式(5 段, UTC)", "default": "0 * * * *"}},
    },
    "trigger.alert_fired": {
        "label": "告警触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {
            "rule": {"type": "text", "label": "告警规则名(子串匹配, 空=全部)", "default": ""},
            "min_severity": {"type": "select", "label": "最低级别",
                             "options": ["info", "warning", "critical"], "default": "warning"},
        },
    },
}

SEVERITY_ORDER = {"info": 0, "warning": 1, "critical": 2}


def exec_trigger(ctx: RunContext, node_id: str, node_type: str, config: dict) -> dict:
    """trigger 节点把触发信息写入 ctx.vars, 下游节点用 {{vars.<node_id>.*}} 引用。

    engine 以 execute(ctx, config) 两参调用, node_id 由注册包装器填充;
    node_id 为空时落到固定键 "trigger" ({{vars.trigger.*}} / {{trigger.*}} 均可用)。
    """
    t = ctx.trigger or {}
    key = node_id or "trigger"
    ctx.vars.setdefault(key, {})
    ctx.vars[key].update({
        "type": node_type,
        "fired_at": t.get("fired_at", ""),
        "payload": t.get("payload", {}),
        "cron": config.get("cron", ""),
        "rule": config.get("rule", ""),
    })
    return {"ok": True, "node_id": key}


def alert_matches(config: dict, rule: str, severity: str) -> bool:
    rule_cfg = (config.get("rule") or "").strip()
    if rule_cfg and rule_cfg not in (rule or ""):
        return False
    min_sev = config.get("min_severity", "warning")
    return SEVERITY_ORDER.get(severity, 0) >= SEVERITY_ORDER.get(min_sev, 0)
