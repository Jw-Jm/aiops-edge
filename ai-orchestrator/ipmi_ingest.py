"""IPMI 上报 ingest — 接收 ipmi-exporter（本地 /dev/ipmi0）上报的传感器数据并落库。

可降级：MySQL 不可用时降级为内存，不阻塞上报。
"""
import db


class IPMIStore:
    """IPMI 传感器存储。"""

    def __init__(self):
        self._mem: list[dict] = []

    def ingest(self, node: str, sensors: list):
        """上报某节点的传感器列表。"""
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
