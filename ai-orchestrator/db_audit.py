"""AuditStore — 审计日志持久化。MySQL 不可用降级为内存。

注意：审计写入发生在 FastAPI async handler 中。dbutils.PooledDB 的
连接在线程池/协程交错场景下可能复用被污染连接导致静默失败，
因此这里为每次写入独立建立 pymysql 连接，写完即关，避开池化问题。
"""
import json
import os
import time
import pymysql

_CFG = None


def _mysql_cfg():
    """读取 MySQL 连接配置（与 db.py 同源 env）。"""
    global _CFG
    if _CFG is None:
        _CFG = {
            "host": os.environ.get("MYSQL_HOST", "127.0.0.1"),
            "port": int(os.environ.get("MYSQL_PORT", "3306")),
            "user": os.environ.get("MYSQL_USER", "root"),
            "password": os.environ.get("MYSQL_PASSWORD", ""),
            "database": os.environ.get("MYSQL_DB", "aiops"),
        }
    return _CFG


class AuditStore:
    def __init__(self):
        self._mem: list[dict] = []

    def log(self, action: str, operator: str, target: str, command: str,
            result: str, detail: dict = None, task_id: str = ""):
        entry = {
            "task_id": task_id, "action": action, "operator": operator,
            "target_service": target, "command": command, "result": result,
            "detail": json.dumps(detail, ensure_ascii=False) if detail else "",
            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        # 独立短连接写入，规避 PooledDB 在 async 上下文被污染的连接
        try:
            cfg = _mysql_cfg()
            conn = pymysql.connect(host=cfg["host"], port=cfg["port"], user=cfg["user"],
                                   password=cfg["password"], database=cfg["database"],
                                   charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO audit_logs (task_id, action, operator, target_service, command, result, detail) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s)",
                        (entry["task_id"], entry["action"], entry["operator"],
                         entry["target_service"], entry["command"], entry["result"], entry["detail"]),
                    )
                conn.commit()
            finally:
                try:
                    conn.close()
                except Exception:
                    pass
        except Exception:
            pass  # 审计失败不影响主流程，降级为内存
        self._mem.append(entry)

    def query(self, page=1, size=50, action=None, operator=None, service=None):
        offset = (page - 1) * size
        try:
            cfg = _mysql_cfg()
            conn = pymysql.connect(host=cfg["host"], port=cfg["port"], user=cfg["user"],
                                   password=cfg["password"], database=cfg["database"],
                                   charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
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
            finally:
                try:
                    conn.close()
                except Exception:
                    pass
        except Exception:
            pass
        mem = self._mem
        if action:
            mem = [e for e in mem if e["action"] == action]
        if operator:
            mem = [e for e in mem if e["operator"] == operator]
        if service:
            mem = [e for e in mem if e["target_service"] == service]
        return {"items": mem[offset:offset + size], "total": len(mem)}

    def query_by_task(self, task_id: str):
        """查询某会话(task_id)的执行记录（处置建议执行历史）。"""
        try:
            cfg = _mysql_cfg()
            conn = pymysql.connect(host=cfg["host"], port=cfg["port"], user=cfg["user"],
                                   password=cfg["password"], database=cfg["database"],
                                   charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "SELECT task_id, action, target_service, command, result, detail, created_at "
                        "FROM audit_logs WHERE task_id=%s ORDER BY id ASC",
                        (task_id,),
                    )
                    return [dict(r) for r in cur.fetchall()]
            finally:
                try:
                    conn.close()
                except Exception:
                    pass
        except Exception:
            pass
        return [e for e in self._mem if e["task_id"] == task_id]
