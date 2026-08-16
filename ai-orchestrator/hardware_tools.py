"""二期强化硬件/部件查询工具 — 供 AI agent 调用，查询 IPMI/部件可用性。

这些工具为只读（Class=safe），查询已采集的数据，不直接操作设备。
"""
from skill_registry import ToolRegistry


@ToolRegistry.register(
    name="ipmi_health",
    description="查询服务器硬件健康（IPMI 传感器：温度/风扇/电压/电源）",
    category="infra",
    params={"node": {"type": "string", "required": True, "desc": "节点名"}},
    cls_="safe", scope="manager",
    when_to_use="用户询问服务器硬件温度、风扇、电压、电源健康时",
)
def ipmi_health(node: str):
    from ipmi_ingest import IPMIStore
    sensors = IPMIStore().query(node=node)
    abnormal = [s for s in sensors if s.get("status") not in ("ok", "")]
    return {"node": node, "sensor_count": len(sensors),
            "abnormal": abnormal, "sensors": sensors[:50]}


@ToolRegistry.register(
    name="node_health",
    description="查询服务器部件可用性（CPU/内存/磁盘/网卡）",
    category="infra",
    params={"node": {"type": "string", "required": False, "desc": "节点名"}},
    cls_="safe", scope="manager",
    when_to_use="用户询问服务器 CPU/内存/磁盘/网卡部件可用性、健康状态时",
)
def node_health(node: str = ""):
    from node_health import NodeHealthAggregator
    rows = NodeHealthAggregator().query(node=node or None)
    # 按节点分组
    grouped = {}
    for r in rows:
        grouped.setdefault(r.get("node_name"), {})[r.get("component")] = r.get("status")
    return {"nodes": grouped}
