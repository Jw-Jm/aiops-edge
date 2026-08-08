"""ApprovalStore — 审批任务持久化。MySQL 不可用降级为内存。"""
import db


class ApprovalStore:
    """审批任务持久化。MySQL 不可用降级为内存。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}

    def _available(self):
        return db.db_available()

    def create(self, task: dict):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO approval_tasks (task_id, service_name, status, plan, script, "
                        "risk_score, risk_reason, diagnosis, report, requester, created_at, decided_at, decision_by) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,NULL,NULL)",
                        (task.get("id"), task.get("service", ""), task.get("status", "waiting"),
                         task.get("plan", ""), task.get("script", ""),
                         float(task.get("risk_score", 0) or 0), task.get("risk_reason", ""),
                         task.get("diagnosis", ""), task.get("report", ""),
                         task.get("requester", ""), task.get("created_at", "")),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem[task["id"]] = dict(task)

    def get(self, task_id: str):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM approval_tasks WHERE task_id=%s", (task_id,))
                    row = cur.fetchone()
                if row:
                    return self._row_to_task(row)
            except Exception:
                pass
            finally:
                conn.close()
        return self._mem.get(task_id)

    def list(self):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM approval_tasks ORDER BY created_at DESC LIMIT 200")
                    rows = cur.fetchall()
                if rows:
                    return [self._row_to_task(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        return list(self._mem.values())

    def update(self, task_id: str, **fields):
        if self._available():
            conn = db.get_conn()
            try:
                cols = []
                vals = []
                for k, v in fields.items():
                    cols.append(f"{k}=%s")
                    vals.append(v)
                vals.append(task_id)
                with conn.cursor() as cur:
                    cur.execute(f"UPDATE approval_tasks SET {', '.join(cols)} WHERE task_id=%s", tuple(vals))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        if task_id in self._mem:
            self._mem[task_id].update(fields)

    def decide(self, task_id: str, status: str, decision_by: str = ""):
        import time
        self.update(task_id, status=status, decided_at=time.strftime("%Y-%m-%dT%H:%M:%SZ"), decision_by=decision_by)

    @staticmethod
    def _row_to_task(row: dict) -> dict:
        return {
            "id": row["task_id"], "status": row["status"], "source": row.get("source", ""),
            "service": row["service_name"], "context": row.get("context", ""),
            "diagnosis": row.get("diagnosis", ""), "plan": row.get("plan", ""),
            "script": row.get("script", ""), "risk_score": row.get("risk_score", 0),
            "risk_reason": row.get("risk_reason", ""), "report": row.get("report", ""),
            "created_at": row.get("created_at", ""), "done_at": row.get("decided_at", ""),
        }
