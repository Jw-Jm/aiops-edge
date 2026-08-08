#!/usr/bin/env python3
"""ipmi-exporter 采集脚本 — 本地读取 IPMI 传感器并通过管理网上报。

原理：
- IPMI 除网络通道（RMCP/带外）外，支持本地 KCS 接口 (/dev/ipmi0)。
- 本容器以 privileged + hostPath 挂载 /dev/ipmi0，在服务器本地用 ipmitool
  读 BMC 传感器（温度/风扇/电压/电源）与 SEL 事件，**不依赖带外网**。
- 采集结果经管理网 POST 到 ai-orchestrator /api/v1/ipmi/ingest。

采集指标（ipmitool sensor）：
- Temperature / Fan / Voltage / Power Supply 等传感器读数与状态
- SEL（系统事件日志）可选
"""
import json
import os
import subprocess
import sys
import time
import urllib.request

NODE_NAME = os.environ.get("NODE_NAME", os.uname().nodename)
ORCHESTRATOR_URL = os.environ.get("ORCHESTRATOR_URL", "http://ai-orchestrator:8000/api/v1/ipmi/ingest")
COLLECT_INTERVAL = int(os.environ.get("COLLECT_INTERVAL", "120"))
IPMITOOL = os.environ.get("IPMITOOL", "ipmitool")

# ipmitool 可能不存在或无权限时，可降级为跳过采集
_SENSOR_TYPE_MAP = {
    "Temperature": "Temperature",
    "Fan": "Fan",
    "Voltage": "Voltage",
    "Current": "Voltage",
    "Power Supply": "Power",
    "Processor": "Temperature",
}


def _run_ipmitool(args):
    """执行 ipmitool，返回 stdout；失败返回 None（可降级）。"""
    try:
        out = subprocess.run(
            [IPMITOOL] + args,
            capture_output=True, text=True, timeout=30,
        )
        if out.returncode == 0:
            return out.stdout
        return None
    except Exception:
        return None


def _parse_sensor_line(line: str):
    """解析 ipmitool sensor 输出行：
    'CPU Temp      | 42.000    | degrees C  | ok    | ...'"""
    parts = [p.strip() for p in line.split("|")]
    if len(parts) < 4:
        return None
    name = parts[0]
    reading = parts[1]
    unit = parts[2]
    status = parts[3].lower()
    sensor_type = _classify_sensor(name, unit)
    return {
        "name": name,
        "reading": f"{reading} {unit}".strip(),
        "type": sensor_type,
        "status": "ok" if status in ("ok", "nr", "na") else (status or "unknown"),
    }


def _classify_sensor(name: str, unit: str):
    nl = name.lower()
    if any(k in nl for k in ("temp", "cpu temp", "ambient")):
        return "Temperature"
    if "fan" in nl:
        return "Fan"
    if any(k in nl for k in ("volt", "vcore", "12v", "5v", "3.3v")):
        return "Voltage"
    if "power" in nl or "psu" in nl or "ps " in nl:
        return "Power"
    return _SENSOR_TYPE_MAP.get(unit, "Health")


def collect_sensors():
    """采集传感器列表。ipmitool 不可用返回 []（可降级）。"""
    out = _run_ipmitool(["sensor", "list"])
    if not out:
        return []
    sensors = []
    for line in out.splitlines():
        s = _parse_sensor_line(line)
        if s:
            sensors.append(s)
    return sensors


def collect_sel():
    """采集 SEL 事件（可选）。返回 [] 降级。"""
    out = _run_ipmitool(["sel", "list", "last", "20"])
    if not out:
        return []
    sel = []
    for line in out.splitlines():
        parts = [p.strip() for p in line.split("|")]
        if len(parts) >= 3:
            sel.append({
                "event_id": parts[0],
                "event_time": parts[1],
                "event_desc": "|".join(parts[2:]),
                "severity": "warning",
            })
    return sel


def report(node, sensors, sel):
    """上报到 orchestrator ingest。失败不阻塞。"""
    payload = {
        "node": node,
        "sensors": sensors,
        "sel_events": sel,
    }
    try:
        req = urllib.request.Request(
            ORCHESTRATOR_URL,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            if resp.status == 200:
                print(f"[{node}] reported {len(sensors)} sensors", flush=True)
    except Exception as e:
        print(f"[{node}] report failed: {e}", flush=True)


def main():
    print(f"ipmi-exporter starting on node={NODE_NAME} interval={COLLECT_INTERVAL}s", flush=True)
    while True:
        sensors = collect_sensors()
        sel = collect_sel()
        if sensors or sel:
            report(NODE_NAME, sensors, sel)
        else:
            print(f"[{NODE_NAME}] no ipmi devices (skip)", flush=True)
        time.sleep(COLLECT_INTERVAL)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(0)
