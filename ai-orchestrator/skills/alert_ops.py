"""Skill: alert_ops — 告警处置（告警规则/事件查询与分析）"""
from __future__ import annotations

import json
import os
import urllib.request
import urllib.error

from contracts import RequestContext
from internal_query import signed_query_api_request
from skill_registry import SkillDef, SkillRegistry, ToolRegistry
from trusted_context import TrustedContextError

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")


def _get_json(url: str, *, request_context: RequestContext | None = None) -> dict:
    try:
        return json.loads(
            signed_query_api_request(url, context=request_context)
        )
    except TrustedContextError as exc:
        return {"error": exc.error_code}
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


def alert_rules(*, request_context: RequestContext | None = None):
    """查询全部告警规则"""
    try:
        data = _get_json(f"{QUERY_API}/alerts/rules", request_context=request_context)
        rules = data.get("data", [])
        if not rules:
            return "暂无告警规则"
        lines = ["告警规则:"]
        for r in rules:
            lines.append(f"- [{r.get('enabled') and '启用' or '禁用'}] {r.get('name','?')} | 服务:{r.get('service','?')} | {r.get('metric','?')} {r.get('condition','>')} {r.get('threshold','?')} | {r.get('severity','')}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询失败: {e}"


def alert_events(limit: int = 10, *, request_context: RequestContext | None = None):
    """查询最近告警事件（聚合）"""
    try:
        data = _get_json(
            f"{QUERY_API}/alerts/events?limit={limit}",
            request_context=request_context,
        )
        events = data.get("data", [])
        if not events:
            return "暂无告警事件"
        lines = ["最近告警事件:"]
        for e in events:
            lines.append(f"- [{e.get('severity','')}] {e.get('rule_name','?')} | 服务:{e.get('service','?')} | 次数:{e.get('count',1)} | 最近:{e.get('last_timestamp','')[:19]}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询失败: {e}"


def _post(
    url: str,
    payload: dict = None,
    *,
    request_context: RequestContext | None = None,
):
    """带鉴权的 POST 请求（用于 ack/resolve/notification）。"""
    data = json.dumps(payload or {}).encode()
    try:
        return json.loads(
            signed_query_api_request(
                url,
                context=request_context,
                data=data,
                method="POST",
                headers={"Content-Type": "application/json"},
            )
        )
    except TrustedContextError as exc:
        return {"error": exc.error_code}
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


def incident_query(limit: int = 20, *, request_context: RequestContext | None = None):
    """查询当前未解决的告警事件（incident，即状态非 resolved 的告警）"""
    try:
        data = _get_json(
            f"{QUERY_API}/alerts/events?limit={limit}",
            request_context=request_context,
        )
        events = data.get("data", [])
        open_ones = [e for e in events if e.get("status") != "resolved"]
        if not open_ones:
            return "当前无未解决的告警事件（incident）"
        lines = ["当前 incident（未解决告警）:"]
        for e in open_ones:
            lines.append(f"- [{e.get('severity','')}] {e.get('rule_name','?')} | 服务:{e.get('service','?')} | 状态:{e.get('status','firing')} | id:{e.get('id','')}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询失败: {e}"


def incident_ack(event_id: str, *, request_context: RequestContext | None = None):
    """确认（ack）一个告警事件 incident，标记为已认领"""
    if not event_id:
        return "缺少 event_id"
    res = _post(
        f"{QUERY_API}/alerts/events/{event_id}/ack",
        {"by": "ai-orchestrator"},
        request_context=request_context,
    )
    if res.get("error"):
        return f"确认失败: {res.get('error')}"
    return f"已确认告警事件 {event_id}（acknowledged）"


def incident_resolve(event_id: str, *, request_context: RequestContext | None = None):
    """解决（resolve）一个告警事件 incident，标记为已恢复"""
    if not event_id:
        return "缺少 event_id"
    res = _post(
        f"{QUERY_API}/alerts/events/{event_id}/resolve",
        {"by": "ai-orchestrator"},
        request_context=request_context,
    )
    if res.get("error"):
        return f"解决失败: {res.get('error')}"
    return f"已解决告警事件 {event_id}（resolved）"


def notification_send(webhook_url: str = "", message: str = ""):
    """发送告警通知到 webhook（规则级或自定义 URL），message 为通知内容"""
    url = webhook_url or os.environ.get("ALERT_WEBHOOK_URL", "")
    if not url:
        return "未配置 webhook_url，且无全局 ALERT_WEBHOOK_URL"
    try:
        req = urllib.request.Request(url, data=json.dumps({"message": message}).encode(),
                                     method="POST", headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=10) as resp:
            return f"通知已发送，HTTP {resp.status}"
    except Exception as e:
        return f"发送失败: {e}"


def register_alert_skill():
    if not ToolRegistry.get("alert_rules"):
        ToolRegistry.register(name="alert_rules",
                              description="查询全部告警规则及阈值配置",
                              category="alert",
                              params={})(alert_rules)
    if not ToolRegistry.get("alert_events"):
        ToolRegistry.register(name="alert_events",
                              description="查询最近告警事件（按规则聚合，含触发次数）",
                              category="alert",
                              params={"limit": {"type": "int", "required": False, "default": 10, "desc": "返回条数"}})(alert_events)
    if not ToolRegistry.get("incident_query"):
        ToolRegistry.register(name="incident_query",
                              description="查询当前未解决的告警事件（incident）",
                              category="alert",
                              params={"limit": {"type": "int", "required": False, "default": 20, "desc": "返回条数"}})(incident_query)
    if not ToolRegistry.get("incident_ack"):
        ToolRegistry.register(name="incident_ack",
                              description="确认（ack）一个告警事件，标记为已认领",
                              category="alert",
                              params={"event_id": {"type": "str", "required": True, "desc": "告警事件 ID"}})(incident_ack)
    if not ToolRegistry.get("incident_resolve"):
        ToolRegistry.register(name="incident_resolve",
                              description="解决（resolve）一个告警事件，标记为已恢复",
                              category="alert",
                              params={"event_id": {"type": "str", "required": True, "desc": "告警事件 ID"}})(incident_resolve)
    if not ToolRegistry.get("notification_send"):
        ToolRegistry.register(name="notification_send",
                              description="发送告警通知到 webhook",
                              category="alert",
                              params={"webhook_url": {"type": "str", "required": False, "desc": "目标 webhook（默认全局）"},
                                      "message": {"type": "str", "required": False, "desc": "通知内容"}})(notification_send)

    SkillRegistry.register(SkillDef(
        name="skill.alert_ops",
        title="告警处置",
        description="查询和分析告警规则与告警事件，理解当前告警态势，给出处置建议并可对 incident 执行确认/解决/通知",
        intent_keywords=["告警", "报警", "alert", "触发", "预警", "事件", "通知", "incident"],
        tools=["alert_rules", "alert_events", "incident_query", "incident_ack", "incident_resolve", "notification_send"],
        system_prompt=(
            "你擅长告警分析与处置。基于告警规则和告警事件数据，分析当前告警态势、"
            "识别高严重性告警并给出优先级处置建议。对未解决的 incident 可调用 "
            "incident_ack 确认、incident_resolve 解决、notification_send 发送通知，直接输出结论。"
        ),
    ))
