"""服务器部件可用性聚合 — 聚合 node_exporter（OS 层）+ IPMI（硬件层）。

部件：cpu / memory / disk / network
状态：healthy / degraded / fault

判定逻辑（可配置阈值）：
- CPU：使用率 < 90 且温度 < 85 且电源 ok → healthy；温度 >= 85 → degraded；使用率 >= 95 → degraded
- 内存：可用率 >= 20% → healthy；< 10% → degraded；OOM/极低 → fault
- 磁盘：disk_ok True → healthy；False → fault
- 网络：net_ok True → healthy；False → fault

P1-3: 真实聚合管道 —— 每 60s 由 main.py 调度器触发 aggregate_all()：
从 VictoriaMetrics（node-exporter/categraf 指标）拉 OS 层指标，从 MySQL ipmi_sensors
拉硬件层最新温度/电源状态，按阈值判定并落库 node_component_health。
VM 访问优先直连 VICTORIA_METRICS_URL，未配置时仅在收到显式授权上下文后
走 query-api 的 /metrics/query_range 代理。
"""
from __future__ import annotations

import json
import logging
import os
import re
import time
import urllib.parse
import urllib.request

import db
from contracts import RequestContext
from internal_query import signed_query_api_request

logger = logging.getLogger("node_health")

# 阈值
_CPU_UTIL_DEGRADED = 95
_CPU_TEMP_DEGRADED = 85
_MEM_AVAIL_PCT_DEGRADED = 10
_MEM_AVAIL_PCT_FAULT = 5
_DISK_AVAIL_PCT_FAULT = 10  # 磁盘可用率低于该值判 fault

# VM 访问：优先直连 VICTORIA_METRICS_URL；空则走 query-api 代理
_QUERY_API_URL = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
_VM_DIRECT_URL = os.environ.get("VICTORIA_METRICS_URL", "")

# node-exporter 暴露端口（instance 形如 <host>:9100）
_NODE_EXPORTER_PORT = os.environ.get("NODE_EXPORTER_PORT", "9100")


def _parse_float(text):
    """从 '42 C' / '92%' 等文本提取数值；无法解析返回 None。"""
    if text is None:
        return None
    m = re.search(r"[-+]?\d+(?:\.\d+)?", str(text))
    return float(m.group(0)) if m else None


def _vm_query_range(
    query: str,
    start: float,
    end: float,
    step: int = 60,
    *,
    request_context: RequestContext | None = None,
) -> list:
    """查询 VictoriaMetrics 指标序列，返回 VM 原生 result 列表（series）。

    优先直连 VICTORIA_METRICS_URL（/api/v1/query_range）；未配置时走
    query-api 的 /api/v1/metrics/query_range PromQL 代理（服务凭证 + 签名上下文）。
    失败抛异常，由调用方降级。
    """
    q = urllib.parse.quote(query)
    params = f"query={q}&start={int(start)}&end={int(end)}&step={int(step)}"
    if _VM_DIRECT_URL:
        url = f"{_VM_DIRECT_URL.rstrip('/')}/api/v1/query_range?{params}"
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=10) as resp:
            body = resp.read()
    else:
        url = f"{_QUERY_API_URL.rstrip('/')}/metrics/query_range?{params}"
        body = signed_query_api_request(url, context=request_context)
    data = json.loads(body.decode("utf-8", errors="replace"))
    result = data.get("data", {}).get("result", [])
    return result if isinstance(result, list) else []


def _last_value(series: dict) -> float:
    """取 query_range 序列最后一个采样值；无数据返回 0.0。"""
    vals = series.get("values") or []
    if not vals:
        return 0.0
    try:
        return float(vals[-1][1])
    except Exception:
        return 0.0


def _series_values(series: list, lo: float = None, hi: float = None) -> list:
    """收集 series 末值，可指定合理区间过滤（排除 0/越界噪声）。"""
    out = []
    for s in series:
        v = _last_value(s)
        if lo is not None and v < lo:
            continue
        if hi is not None and v > hi:
            continue
        out.append(v)
    return out


def _discover_nodes(*, request_context: RequestContext | None = None) -> list:
    """从 VM node_cpu_seconds_total 的 instance 标签发现节点名列表。"""
    now = time.time()
    series = _vm_query_range(
        'node_cpu_seconds_total{mode="idle"}',
        now - 300,
        now,
        step=60,
        request_context=request_context,
    )
    nodes = set()
    for s in series:
        inst = (s.get("metric") or {}).get("instance", "")
        host = inst.split(":")[0] if inst else ""
        if host:
            nodes.add(host)
    return sorted(nodes)


def _vm_node_metrics(
    node: str, *, request_context: RequestContext | None = None
) -> dict:
    """从 VM 拉取单节点 OS 层指标。返回 {cpu_util, mem_avail_pct, disk_ok, disk_avail_pct}。

    node-exporter/categraf 的 instance 标签形如 <host>:9100（端口可用 NODE_EXPORTER_PORT 覆盖）。
    任一指标查询失败/无数据时保持默认健康值，不阻塞整轮聚合。
    """
    now = time.time()
    start, end, step = now - 300, now, 60
    inst = f'instance="{node}:{_NODE_EXPORTER_PORT}"'
    m = {"cpu_util": 0.0, "mem_avail_pct": 100.0, "disk_ok": True, "disk_avail_pct": 100.0}

    # CPU 使用率 = 100 - idle 占比
    try:
        cpu = _series_values(
            _vm_query_range(
                f'100 - avg(rate(node_cpu_seconds_total{{mode="idle",{inst}}}[5m])) by (instance) * 100',
                start, end, step, request_context=request_context), lo=0.0, hi=100.0)
        if cpu:
            m["cpu_util"] = round(sum(cpu) / len(cpu), 2)
    except Exception as e:
        logger.debug("[node_health] cpu query failed node=%s: %s", node, e)

    # 内存可用率 = MemAvailable / MemTotal * 100
    try:
        mem = _series_values(
            _vm_query_range(
                f'(avg(node_memory_MemAvailable_bytes{{{inst}}}) by (instance) '
                f'/ avg(node_memory_MemTotal_bytes{{{inst}}}) by (instance)) * 100',
                start, end, step, request_context=request_context), lo=0.0, hi=100.0)
        if mem:
            m["mem_avail_pct"] = round(sum(mem) / len(mem), 2)
    except Exception as e:
        logger.debug("[node_health] mem query failed node=%s: %s", node, e)

    # 磁盘可用率 = avail / size * 100（排除 rootfs/tmpfs 伪设备）
    try:
        disk = _series_values(
            _vm_query_range(
                f'(sum(node_filesystem_avail_bytes{{{inst},fstype!="rootfs",fstype!="tmpfs"}}) by (instance) '
                f'/ sum(node_filesystem_size_bytes{{{inst},fstype!="rootfs",fstype!="tmpfs"}}) by (instance)) * 100',
                start, end, step, request_context=request_context), lo=0.0, hi=100.0)
        if disk:
            pct = sum(disk) / len(disk)
            m["disk_avail_pct"] = round(pct, 2)
            m["disk_ok"] = pct >= _DISK_AVAIL_PCT_FAULT
    except Exception as e:
        logger.debug("[node_health] disk query failed node=%s: %s", node, e)

    return m


def _ipmi_node_metrics(node: str) -> dict:
    """从 MySQL ipmi_sensors 读该节点最新温度/电源状态。返回 {cpu_temp, power_ok}。"""
    m = {"cpu_temp": None, "power_ok": True}
    if not db.db_available():
        return m
    conn = db.get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT sensor_name, sensor_type, reading, status FROM ipmi_sensors "
                "WHERE node_name=%s ORDER BY id DESC LIMIT 200", (node,))
            rows = cur.fetchall()
    except Exception:
        rows = []
    finally:
        try:
            conn.close()
        except Exception:
            pass
    temps, powers = [], []
    for r in rows or []:
        stype = (r.get("sensor_type") or "").lower()
        sname = (r.get("sensor_name") or "").lower()
        reading = r.get("reading") or ""
        if "temp" in stype or "temp" in sname or "ambient" in sname:
            val = _parse_float(reading)
            if val is not None:
                temps.append(val)
        if "power" in stype or "psu" in sname or "power" in sname or "power supply" in sname:
            powers.append(r)
    if temps:
        m["cpu_temp"] = max(temps)
    if powers:
        ok = True
        for r in powers:
            text = f"{r.get('reading', '')} {r.get('status', '')}".lower()
            if (r.get("status") not in ("ok", "nr", "na", None, "")
                    or any(k in text for k in ("off", "fail", "lost", "absent", "critical", "non-recov"))):
                ok = False
        m["power_ok"] = ok
    return m


class NodeHealthAggregator:
    """部件可用性聚合器（P1-3: 真实聚合管道）。"""

    def aggregate(self, node: str, metrics: dict, detail: dict = None) -> dict:
        """输入 node_exporter + IPMI 指标，输出各部件状态（并落库）。"""
        result = {
            "node": node,
            "cpu": "healthy",
            "memory": "healthy",
            "disk": "healthy",
            "network": "healthy",
        }

        # CPU
        cpu_util = float(metrics.get("cpu_util", 0) or 0)
        cpu_temp = float(metrics.get("cpu_temp", 0) or 0)
        if cpu_util >= _CPU_UTIL_DEGRADED or cpu_temp >= _CPU_TEMP_DEGRADED:
            result["cpu"] = "degraded"
        if not metrics.get("power_ok", True):
            result["cpu"] = "fault"

        # 内存
        mem_pct = float(metrics.get("mem_avail_pct", 100) or 100)
        if mem_pct <= _MEM_AVAIL_PCT_FAULT:
            result["memory"] = "fault"
        elif mem_pct <= _MEM_AVAIL_PCT_DEGRADED:
            result["memory"] = "degraded"

        # 磁盘
        result["disk"] = "healthy" if metrics.get("disk_ok", True) else "fault"

        # 网络
        result["network"] = "healthy" if metrics.get("net_ok", True) else "fault"

        self._persist(node, result, detail or {})
        return result

    def _persist(self, node: str, status: dict, detail: dict = None):
        """落库 node_component_health（表无唯一键：按 node+component 查后 更新/插入，避免重复行）。
        MySQL 不可用降级。"""
        if not db.db_available():
            return
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                for comp in ["cpu", "memory", "disk", "network"]:
                    d = ((detail or {}).get(comp) or "")[:255]
                    cur.execute(
                        "SELECT id FROM node_component_health "
                        "WHERE node_name=%s AND component=%s ORDER BY id DESC LIMIT 1",
                        (node, comp))
                    row = cur.fetchone()
                    if row:
                        cur.execute(
                            "UPDATE node_component_health SET status=%s, detail=%s WHERE id=%s",
                            (status[comp], d, row["id"]))
                    else:
                        cur.execute(
                            "INSERT INTO node_component_health (node_name, component, status, detail) "
                            "VALUES (%s,%s,%s,%s)",
                            (node, comp, status[comp], d))
            conn.commit()
        except Exception:
            pass
        finally:
            try:
                conn.close()
            except Exception:
                pass

    def collect_node_metrics(
        self, node: str, *, request_context: RequestContext | None = None
    ) -> dict:
        """采集单节点真实指标：VM（OS 层）+ MySQL ipmi_sensors（硬件层）。"""
        metrics = _vm_node_metrics(node, request_context=request_context)
        metrics.update(_ipmi_node_metrics(node))
        # 网络：暂无独立采集源，默认 ok（node_network 可后续接入）
        metrics.setdefault("net_ok", True)
        return metrics

    def aggregate_all(
        self,
        nodes: list = None,
        *,
        request_context: RequestContext | None = None,
    ) -> list:
        """真实聚合管道：从 VM 发现节点（或指定节点），逐节点采集真实指标、
        按阈值判定部件状态、落库 node_component_health，并对异常节点写审计+日志。

        返回 [{node, metrics, components}, ...]。
        """
        node_list = [n.strip() for n in (nodes or []) if (n or "").strip()]
        if not node_list:
            try:
                node_list = _discover_nodes(request_context=request_context)
            except Exception as e:
                logger.warning("[node_health] 节点发现失败(VM 不可达或空数据): %s", e)
                node_list = []
        results = []
        for node in node_list:
            try:
                metrics = self.collect_node_metrics(
                    node, request_context=request_context
                )
                detail = {
                    "cpu": (f"util={metrics.get('cpu_util', 0):.1f}% "
                            f"temp={metrics.get('cpu_temp') if metrics.get('cpu_temp') is not None else '-'}"
                            f" power_ok={metrics.get('power_ok', True)}"),
                    "memory": f"avail={metrics.get('mem_avail_pct', 0):.1f}%",
                    "disk": f"avail={metrics.get('disk_avail_pct', 0):.1f}%",
                    "network": "ok",
                }
                components = self.aggregate(node, metrics, detail)
                results.append({"node": node, "metrics": metrics, "components": components})
                self._alert_if_unhealthy(node, components, metrics)
            except Exception as e:
                logger.warning("[node_health] 节点 %s 聚合失败: %s", node, e)
        return results

    def _alert_if_unhealthy(self, node: str, status: dict, metrics: dict):
        """异常部件告警：写 audit + 打日志（部件状态已由 _persist 落库 node_component_health）。

        跨服务告警引擎事件不现实，沿用「落库状态 + audit + 日志」最简路径。
        """
        abnormal = {k: v for k, v in status.items()
                    if k in ("cpu", "memory", "disk", "network") and v != "healthy"}
        if not abnormal:
            return
        summary = ", ".join(f"{k}={v}" for k, v in abnormal.items())
        detail = (f"cpu_util={metrics.get('cpu_util', 0):.1f}% "
                  f"temp={metrics.get('cpu_temp')} "
                  f"mem_avail={metrics.get('mem_avail_pct', 0):.1f}% "
                  f"disk_avail={metrics.get('disk_avail_pct', 0):.1f}% "
                  f"power_ok={metrics.get('power_ok', True)}")
        logger.warning("[node_health] 节点 %s 异常部件: %s (%s)", node, summary, detail)
        try:
            # 延迟导入避免 import 环（orchestrator 未依赖本模块，但保持安全）
            from orchestrator import _audit_log
            _audit_log(f"node:{node}", "node_health_alert", "system",
                       node, detail[:300], "alert", {"components": abnormal})
        except Exception:
            pass

    def query(self, node: str = None) -> list:
        """查询部件可用性。"""
        if db.db_available():
            conn = db.get_conn()
            try:
                w = " WHERE node_name=%s" if node else ""
                vals = (node,) if node else ()
                with conn.cursor() as cur:
                    cur.execute("SELECT node_name, component, status, detail, updated_at FROM node_component_health" + w + " ORDER BY node_name, component", vals)
                    rows = cur.fetchall()
                if rows:
                    return [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                try:
                    conn.close()
                except Exception:
                    pass
        return []
