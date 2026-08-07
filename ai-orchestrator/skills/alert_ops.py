"""Skill: alert_ops — 告警处置（告警规则/事件查询与分析）"""
import json
import os
import urllib.request
import urllib.error

from skill_registry import SkillDef, SkillRegistry, ToolRegistry

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
INTERNAL_TOKEN = os.environ.get("INTERNAL_TOKEN", "")


def _get_json(url: str) -> dict:
    req = urllib.request.Request(url, headers={"X-Tenant-ID": "default"})
    if INTERNAL_TOKEN:
        req.add_header("X-Internal-Token", INTERNAL_TOKEN)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


def alert_rules():
    """查询全部告警规则"""
    try:
        data = _get_json(f"{QUERY_API}/alerts/rules")
        rules = data.get("data", [])
        if not rules:
            return "暂无告警规则"
        lines = ["告警规则:"]
        for r in rules:
            lines.append(f"- [{r.get('enabled') and '启用' or '禁用'}] {r.get('name','?')} | 服务:{r.get('service','?')} | {r.get('metric','?')} {r.get('condition','>')} {r.get('threshold','?')} | {r.get('severity','')}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询失败: {e}"


def alert_events(limit: int = 10):
    """查询最近告警事件（聚合）"""
    try:
        data = _get_json(f"{QUERY_API}/alerts/events?limit={limit}")
        events = data.get("data", [])
        if not events:
            return "暂无告警事件"
        lines = ["最近告警事件:"]
        for e in events:
            lines.append(f"- [{e.get('severity','')}] {e.get('rule_name','?')} | 服务:{e.get('service','?')} | 次数:{e.get('count',1)} | 最近:{e.get('last_timestamp','')[:19]}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询失败: {e}"


def register_alert_skill():
    if not ToolRegistry.get("alert_rules"):
        ToolRegistry.register(name="alert_rules",
                              description="查询全部告警规则及阈值配置",
                              category="alert")(alert_rules)
    if not ToolRegistry.get("alert_events"):
        ToolRegistry.register(name="alert_events",
                              description="查询最近告警事件（按规则聚合，含触发次数）",
                              category="alert")(alert_events)

    SkillRegistry.register(SkillDef(
        name="skill.alert_ops",
        title="告警处置",
        description="查询和分析告警规则与告警事件，理解当前告警态势，给出处置建议",
        intent_keywords=["告警", "报警", "alert", "触发", "预警", "事件", "通知"],
        tools=["alert_rules", "alert_events"],
        system_prompt=(
            "你擅长告警分析与处置。基于告警规则和告警事件数据，分析当前告警态势、"
            "识别高严重性告警并给出优先级处置建议，直接输出结论。"
        ),
    ))
