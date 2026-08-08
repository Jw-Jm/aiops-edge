"""服务器部件可用性聚合 — 聚合 node_exporter（OS 层）+ IPMI（硬件层）。

部件：cpu / memory / disk / network
状态：healthy / degraded / fault

判定逻辑（可配置阈值）：
- CPU：使用率 < 90 且温度 < 85 且电源 ok → healthy；温度 >= 85 → degraded；使用率 >= 95 → degraded
- 内存：可用率 >= 20% → healthy；< 10% → degraded；OOM/极低 → fault
- 磁盘：disk_ok True → healthy；False → fault
- 网络：net_ok True → healthy；False → fault
"""
import db

# 阈值
_CPU_UTIL_DEGRADED = 95
_CPU_TEMP_DEGRADED = 85
_MEM_AVAIL_PCT_DEGRADED = 10
_MEM_AVAIL_PCT_FAULT = 5


class NodeHealthAggregator:
    """部件可用性聚合器。"""

    def aggregate(self, node: str, metrics: dict) -> dict:
        """输入 node_exporter + IPMI 指标，输出各部件状态。"""
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

        self._persist(node, result)
        return result

    def _persist(self, node: str, status: dict):
        """落库 node_component_health。MySQL 不可用降级。"""
        if not db.db_available():
            return
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                for comp in ["cpu", "memory", "disk", "network"]:
                    cur.execute(
                        "INSERT INTO node_component_health (node_name, component, status, detail) "
                        "VALUES (%s,%s,%s,%s) ON DUPLICATE KEY UPDATE status=VALUES(status), detail=VALUES(detail)",
                        (node, comp, status[comp], ""),
                    )
            conn.commit()
        except Exception:
            pass
        finally:
            conn.close()

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
                conn.close()
        return []
