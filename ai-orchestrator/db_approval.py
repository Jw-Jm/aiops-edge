"""ApprovalStore — 审批任务持久化。MySQL 不可用降级为内存。

降级语义（fail-loud）：
- 写路径（create/update）失败必须记录日志并标记降级态，不允许静默——
  审批决策是执行链的授权依据，丢失即审计断链。
- 降级期间数据仅存进程内存：重启即丢、多副本互不可见。读路径通过
  degraded() 暴露该状态，API 层可据此提示"非持久/单副本"。
- UPDATE 列名经白名单校验，防止新增调用方把任意字段拼进 SQL。
"""
import logging

import db

logger = logging.getLogger("aiops.db_approval")

# approval_tasks 的合法可更新列（白名单，防动态列名拼接注入）
_UPDATABLE_COLUMNS = frozenset({
    "status", "plan", "script", "risk_score", "risk_reason",
    "diagnosis", "report", "decided_at", "decision_by",
})


class ApprovalStore:
    """审批任务持久化。MySQL 不可用降级为内存（降级态可观测）。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}
        self._degraded = False

    def _available(self):
        return db.db_available()

    def degraded(self) -> bool:
        """是否处于内存降级态（MySQL 写入失败过）。降级期间数据非持久。"""
        return self._degraded

    def _mark_degraded(self, op: str, task_id: str, exc: Exception):
        self._degraded = True
        logger.error(
            "approval store %s failed; falling back to in-memory (non-durable) task_id=%s error_type=%s",
            op, task_id or "-", type(exc).__name__,
        )

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
            except Exception as exc:
                self._mark_degraded("insert", str(task.get("id") or ""), exc)
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
            except Exception as exc:
                # 读失败不标记降级（可能是瞬时抖动），但必须留痕
                logger.warning("approval store read failed task_id=%s error_type=%s",
                               task_id or "-", type(exc).__name__)
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
            except Exception as exc:
                logger.warning("approval store list failed error_type=%s", type(exc).__name__)
            finally:
                conn.close()
        return list(self._mem.values())

    def update(self, task_id: str, **fields):
        unknown = set(fields) - _UPDATABLE_COLUMNS
        if unknown:
            # 无论 DB 分支是否可用都拒绝：内存路径同样不接受未注册字段
            logger.error("approval store update rejected non-whitelisted columns=%s task_id=%s",
                         sorted(unknown), task_id or "-")
            raise ValueError(f"approval update has non-whitelisted columns: {sorted(unknown)}")
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
            except Exception as exc:
                self._mark_degraded("update", task_id, exc)
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
