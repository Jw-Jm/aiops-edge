"""AuditStore — 审计日志持久化。MySQL 不可用降级为内存。"""
import json
import db


class AuditStore:
    def __init__(self):
        self._mem: list[dict] = []

    def log(self, action: str, operator: str, target: str, command: str,
            result: str, detail: dict = None, task_id: str = ""):
        import time
        entry = {
            "task_id": task_id, "action": action, "operator": operator,
            "target_service": target, "command": command, "result": result,
            "detail": json.dumps(detail, ensure_ascii=False) if detail else "",
            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO audit_logs (task_id, action, operator, target_service, command, result, detail) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s)",
                        (entry["task_id"], entry["action"], entry["operator"],
                         entry["target_service"], entry["command"], entry["result"], entry["detail"]),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.append(entry)

    def query(self, page=1, size=50, action=None, operator=None, service=None):
        offset = (page - 1) * size
        if db.db_available():
            conn = db.get_conn()
            try:
                where = []
                vals = []
                if action:
                    where.append("action=%s"); vals.append(action)
                if operator:
                    where.append("operator=%s"); vals.append(operator)
                if service:
                    where.append("target_service=%s"); vals.append(service)
                w = (" WHERE " + " AND ".join(where)) if where else ""
                with conn.cursor() as cur:
                    cur.execute("SELECT COUNT(*) AS total FROM audit_logs" + w, tuple(vals))
                    total = cur.fetchone()["total"]
                    cur.execute("SELECT * FROM audit_logs" + w + " ORDER BY id DESC LIMIT %s OFFSET %s",
                                tuple(vals) + (size, offset))
                    rows = cur.fetchall()
                if rows is not None:
                    return {"items": [dict(r) for r in rows], "total": total}
            except Exception:
                pass
            finally:
                conn.close()
        mem = self._mem
        if action:
            mem = [e for e in mem if e["action"] == action]
        if operator:
            mem = [e for e in mem if e["operator"] == operator]
        if service:
            mem = [e for e in mem if e["target_service"] == service]
        return {"items": mem[offset:offset + size], "total": len(mem)}
