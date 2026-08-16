"""IPMI 上报 ingest — 接收 ipmi-exporter（本地 /dev/ipmi0）上报的传感器数据并落库。

可降级：MySQL 不可用时降级为内存，不阻塞上报。
P1-2: 补写 ipmi_sel_events（SEL 事件明细，此前表建零写入）。
"""
import re
from typing import Optional
import db


def _parse_float(text) -> Optional[float]:
    """从 '42 C' / '6000 RPM' 等读数文本提取数值；无法解析返回 None。"""
    if text is None:
        return None
    m = re.search(r"[-+]?\d+(?:\.\d+)?", str(text))
    return float(m.group(0)) if m else None


def _normalize_event_time(value):
    """把上报的 event_time 归一为 MySQL DATETIME（'%Y-%m-%d %H:%M:%S'）；无法解析返回 None。"""
    if value in (None, ""):
        return None
    s = str(value).strip()
    # 已是 'YYYY-MM-DD HH:MM:SS' / 'YYYY-MM-DDTHH:MM:SS'
    m = re.match(r"^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}", s)
    if m:
        return m.group(0).replace("T", " ")
    # 'MM/DD/YYYY HH:MM:SS'（ipmitool sel list 常见格式）
    m = re.match(r"^(\d{1,2})/(\d{1,2})/(\d{4})[\s]+(\d{2}):(\d{2})(?::(\d{2}))?", s)
    if m:
        mon, day, yr, hh, mm, ss = m.groups()
        return f"{yr}-{int(mon):02d}-{int(day):02d} {hh}:{mm}:{ss or '00'}"
    # 'YYYY/MM/DD HH:MM:SS'
    m = re.match(r"^(\d{4})/(\d{1,2})/(\d{1,2})[\s]+(\d{2}):(\d{2})(?::(\d{2}))?", s)
    if m:
        yr, mon, day, hh, mm, ss = m.groups()
        return f"{yr}-{int(mon):02d}-{int(day):02d} {hh}:{mm}:{ss or '00'}"
    return None


class IPMIStore:
    """IPMI 传感器 + SEL 事件存储。"""

    def __init__(self):
        self._mem: list[dict] = []
        self._mem_sel: list[dict] = []

    def ingest(self, node: str, sensors: list, sel_events: list = None):
        """上报某节点的传感器列表（及可选 SEL 事件）。"""
        for s in sensors or []:
            entry = {
                "node_name": node,
                "sensor_name": s.get("name", ""),
                "sensor_type": s.get("type", ""),
                "reading": s.get("reading", ""),
                "status": s.get("status", "ok"),
            }
            if db.db_available():
                conn = db.get_conn()
                try:
                    with conn.cursor() as cur:
                        cur.execute(
                            "INSERT INTO ipmi_sensors (node_name, sensor_name, sensor_type, reading, status) "
                            "VALUES (%s,%s,%s,%s,%s)",
                            (entry["node_name"], entry["sensor_name"], entry["sensor_type"],
                             entry["reading"], entry["status"]),
                        )
                    conn.commit()
                except Exception:
                    pass
                finally:
                    conn.close()
            self._mem.append(entry)
        if sel_events:
            self.ingest_sel(node, sel_events)

    def ingest_sel(self, node: str, sel_events: list):
        """落库 SEL 事件明细（幂等：同一 node+event_id 不重复插；无 event_id 无法去重直接插入）。"""
        for ev in sel_events or []:
            event_id = str(ev.get("event_id") or ev.get("record_id") or ev.get("id") or "")
            entry = {
                "node_name": node,
                "event_id": event_id,
                "event_time": _normalize_event_time(ev.get("event_time") or ev.get("created_at")
                                                    or ev.get("timestamp")),
                "sensor": ev.get("sensor") or ev.get("sensor_name") or "",
                "event_desc": (ev.get("event_desc") or ev.get("message")
                               or ev.get("description") or "")[:255],
                "severity": ev.get("severity") or "warning",
            }
            if db.db_available():
                conn = db.get_conn()
                try:
                    with conn.cursor() as cur:
                        dup = False
                        if event_id:
                            cur.execute(
                                "SELECT COUNT(*) AS c FROM ipmi_sel_events "
                                "WHERE node_name=%s AND event_id=%s",
                                (node, event_id))
                            dup = (cur.fetchone() or {}).get("c", 0) > 0
                        if not dup:
                            cur.execute(
                                "INSERT INTO ipmi_sel_events (node_name, event_id, event_time, sensor, event_desc, severity) "
                                "VALUES (%s,%s,%s,%s,%s,%s)",
                                (entry["node_name"], entry["event_id"], entry["event_time"],
                                 entry["sensor"], entry["event_desc"], entry["severity"]),
                            )
                            conn.commit()
                except Exception:
                    pass
                finally:
                    conn.close()
            # 内存降级路径同样按 node+event_id 去重
            if event_id and any(e["node_name"] == node and e["event_id"] == event_id
                                for e in self._mem_sel):
                continue
            self._mem_sel.append(entry)

    def query(self, node: str = None, sensor_type: str = None) -> list:
        items = []
        if db.db_available():
            conn = db.get_conn()
            try:
                where, vals = [], []
                if node:
                    where.append("node_name=%s"); vals.append(node)
                if sensor_type:
                    where.append("sensor_type=%s"); vals.append(sensor_type)
                w = (" WHERE " + " AND ".join(where)) if where else ""
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM ipmi_sensors" + w + " ORDER BY id DESC LIMIT 500", tuple(vals))
                    rows = cur.fetchall()
                if rows:
                    items = [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
            if items:
                return items
        items = self._mem
        if node:
            items = [i for i in items if i["node_name"] == node]
        if sensor_type:
            items = [i for i in items if i["sensor_type"] == sensor_type]
        return items

    def query_sel(self, node: str = None, limit: int = 50) -> list:
        """查询 SEL 事件明细（按 event_time 倒序）。"""
        limit = max(1, min(int(limit or 50), 500))
        items = []
        if db.db_available():
            conn = db.get_conn()
            try:
                w = " WHERE node_name=%s" if node else ""
                vals = (node,) if node else ()
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM ipmi_sel_events" + w +
                                " ORDER BY COALESCE(event_time, created_at) DESC, id DESC LIMIT %s",
                                vals + (limit,))
                    rows = cur.fetchall()
                if rows:
                    items = [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
            if items:
                return items
        items = self._mem_sel
        if node:
            items = [i for i in items if i["node_name"] == node]
        return items[:limit]
