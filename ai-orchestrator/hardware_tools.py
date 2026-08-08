"""二期强化硬件/部件查询工具 — 供 AI agent 调用，查询 SNMP/IPMI/部件可用性。

这些工具为只读（Class=safe），查询已采集的数据，不直接操作设备。
"""
from skill_registry import ToolRegistry


@ToolRegistry.register(
    name="snmp_query",
    description="查询网络设备（SNMP 交换机）的接口信息",
    category="infra",
    params={"ip": {"type": "string", "required": True, "desc": "设备 IP"},
            "hostname": {"type": "string", "required": False, "desc": "设备主机名"}},
    cls_="safe", scope="manager",
    when_to_use="用户询问交换机/网络设备的接口、端口状态、流量时",
)
def snmp_query(ip: str = "", hostname: str = ""):
    from db_snmp import SNMPDeviceStore
    store = SNMPDeviceStore()
    devs = store.list(active_only=False)
    dev = None
    if ip:
        dev = next((d for d in devs if d.get("ip") == ip), None)
    elif hostname:
        dev = next((d for d in devs if d.get("hostname") == hostname), None)
    if not dev:
        return {"found": False, "message": "未找到 SNMP 设备"}
    ifaces = store.list_interfaces(dev.get("id"))
    return {"found": True, "device": dev.get("hostname"), "ip": dev.get("ip"),
            "interfaces": [{"name": i.get("if_name"), "status": i.get("if_oper_status"),
                            "in_octets": i.get("if_in_octets"), "out_octets": i.get("if_out_octets")}
                           for i in ifaces]}


@ToolRegistry.register(
    name="snmp_health",
    description="查询网络设备健康状态（SNMP 设备基本信息）",
    category="infra",
    params={"ip": {"type": "string", "required": False, "desc": "设备 IP"}},
    cls_="safe", scope="manager",
    when_to_use="用户询问网络设备健康、状态时",
)
def snmp_health(ip: str = ""):
    from db_snmp import SNMPDeviceStore
    devs = SNMPDeviceStore().list(active_only=False)
    if ip:
        dev = next((d for d in devs if d.get("ip") == ip), None)
        return {"found": dev is not None, "device": dev or {}}
    return {"found": len(devs) > 0, "devices": devs}


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
