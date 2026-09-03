"""ApprovalStore — 审批任务持久化（legacy/兼容，MySQL 唯一权威）。

P1-R1：审批结果作为执行授权依据不得以内存降级充当生产 SoT。本类已移除
内存 fallback——MySQL 不可用或写入失败时操作抛 ApprovalStoreError（fail-closed），
调用方（审批/持久化路径）必须把失败返回给用户，绝不把"内存成功"当作授权。

- 现代 canonical Action 审批/执行链在 query-api（ai_actions/ai_approval_decisions，
  MySQL owner），不经过本类（见 query-go workflow_contract_mysql_test.go）。
- 本类仅服务 orchestrator 侧 legacy 任务工作台（approval_tasks），其 approve
  端点已由 main._legacy_approval_compat_enabled 默认关闭（410）。
- UPDATE 列名经白名单校验。
"""
import logging

import db

logger = logging.getLogger("aiops.db_approval")

# approval_tasks 的合法可更新列（白名单，防动态列名拼接注入）
_UPDATABLE_COLUMNS = frozenset({
    "status", "plan", "script", "risk_score", "risk_reason",
    "diagnosis", "report", "decided_at", "decision_by",
})


class ApprovalStoreError(RuntimeError):
    """审批持久化不可用（fail-closed）。调用方应返回 5xx，不得降级授权。"""


class ApprovalStore:
    """审批任务持久化（仅 MySQL）。MySQL 不可用即失败，不做内存降级。"""

    def __init__(self):
        self._degraded = False

    def _available(self):
        return db.db_available()

    def degraded(self) -> bool:
        """是否发生过 MySQL 写入失败。期间审批操作全部失败（fail-closed）。"""
        return self._degraded

    def _fail(self, op: str, task_id: str, exc: Exception):
        self._degraded = True
        logger.error(
            "approval store %s failed (fail-closed, no in-memory fallback) task_id=%s error_type=%s",
            op, task_id or "-", type(exc).__name__,
        )
        raise ApprovalStoreError(f"approval store {op} unavailable (mysql): {type(exc).__name__}")

    def create(self, task: dict):
        if not self._available():
            self._fail("insert", str(task.get("id") or ""), RuntimeError("mysql unavailable"))
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
        except Exception as exc:  # noqa: BLE001 — 错误路径已显式处理（读降级日志/写 raise fail-closed）
            self._fail("insert", str(task.get("id") or ""), exc)
        finally:
            conn.close()

    def get(self, task_id: str):
        if not self._available():
            logger.warning("approval store read unavailable task_id=%s (mysql down)", task_id or "-")
            return None
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT * FROM approval_tasks WHERE task_id=%s", (task_id,))
                row = cur.fetchone()
            if row:
                return self._row_to_task(row)
            return None
        except Exception as exc:  # noqa: BLE001 — 读路径降级日志（授权写路径 fail-closed raise）
            logger.warning("approval store read failed task_id=%s error_type=%s",
                           task_id or "-", type(exc).__name__)
            return None
        finally:
            conn.close()

    def list(self):
        if not self._available():
            logger.warning("approval store list unavailable (mysql down)")
            return []
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT * FROM approval_tasks ORDER BY created_at DESC LIMIT 200")
                rows = cur.fetchall()
            return [self._row_to_task(r) for r in rows]
        except Exception as exc:  # noqa: BLE001 — 读路径降级日志（授权写路径 fail-closed raise）
            logger.warning("approval store list failed error_type=%s", type(exc).__name__)
            return []
        finally:
            conn.close()

    def update(self, task_id: str, **fields):
        unknown = set(fields) - _UPDATABLE_COLUMNS
        if unknown:
            logger.error("approval store update rejected non-whitelisted columns=%s task_id=%s",
                         sorted(unknown), task_id or "-")
            raise ValueError(f"approval update has non-whitelisted columns: {sorted(unknown)}")
        if not self._available():
            self._fail("update", task_id, RuntimeError("mysql unavailable"))
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
        except Exception as exc:  # noqa: BLE001 — 错误路径已显式处理（读降级日志/写 raise fail-closed）
            self._fail("update", task_id, exc)
        finally:
            conn.close()

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
