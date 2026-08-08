"""SNMP 设备与接口存储。MySQL 不可用降级为内存。"""
import time
import db


class SNMPDeviceStore:
    """SNMP 设备 + 接口持久化。"""

    def __init__(self):
        self._mem: dict[int, dict] = {}
        self._seq = 0

    def list(self, active_only: bool = True) -> list:
        if db.db_available():
            conn = db.get_conn()
            try:
                w = " WHERE status='active'" if active_only else ""
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM snmp_devices" + w + " ORDER BY id")
                    rows = cur.fetchall()
                if rows:
                    return [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        mem = [v for v in self._mem.values()]
        if active_only:
            mem = [v for v in mem if v.get("status", "active") == "active"]
        return mem

    def create(self, dev: dict):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO snmp_devices (hostname, ip, community, snmp_version, vendor, model, location, status) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                        (dev.get("hostname"), dev.get("ip"), dev.get("community", "public"),
                         dev.get("snmp_version", "v2c"), dev.get("vendor", ""), dev.get("model", ""),
                         dev.get("location", ""), dev.get("status", "active")),
                    )
                    dev["id"] = cur.lastrowid
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        if "id" not in dev:
            self._seq += 1
            dev["id"] = self._seq
        self._mem[dev["id"]] = dict(dev)
        return dev["id"]

    def delete(self, dev_id: int):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("DELETE FROM snmp_devices WHERE id=%s", (dev_id,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.pop(dev_id, None)

    def save_interfaces(self, dev_id: int, interfaces: list):
        if not interfaces:
            return
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    # 清除旧接口（重采）
                    cur.execute("DELETE FROM network_interfaces WHERE device_id=%s", (dev_id,))
                    for it in interfaces:
                        cur.execute(
                            "INSERT INTO network_interfaces (device_id, if_index, if_name, if_oper_status, "
                            "if_admin_status, if_speed_mbps, if_in_octets, if_out_octets, if_in_errors, if_out_errors) "
                            "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                            (dev_id, it.get("if_index", 0), it.get("if_name", ""),
                             it.get("if_oper_status", ""), it.get("if_admin_status", ""),
                             it.get("if_speed_mbps", 0), it.get("if_in_octets", 0),
                             it.get("if_out_octets", 0), it.get("if_in_errors", 0), it.get("if_out_errors", 0)),
                        )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()

    def list_interfaces(self, dev_id: int) -> list:
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM network_interfaces WHERE device_id=%s ORDER BY if_index", (dev_id,))
                    rows = cur.fetchall()
                if rows:
                    return [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        return []

    def touch_collect(self, dev_id: int):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("UPDATE snmp_devices SET last_collect_at=NOW() WHERE id=%s", (dev_id,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
