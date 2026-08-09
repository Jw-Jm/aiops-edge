"""产物中心聚合（C3）。

统一聚合 reports / approval_tasks / flow_runs 三类产物，输出统一结构：
    {type, id, title, status, service, time, summary, detail_url}

- reports / approval_tasks 存 MySQL（aiops 库，orchestrator 拥有）
- flow_runs 存 SQLite（flow_engine/store.py）

MySQL 不可用时静默降级（返回空/内存数据）。
"""
import db

# 统一产物结构常量
REPORT = "report"
APPROVAL = "approval"
FLOW_RUN = "flow_run"


def _normalize_time(t):
    """把各类时间格式转为可排序的 ISO 字符串。"""
    if not t:
        return ""
    return str(t)


def list_reports(limit=50):
    """从 MySQL reports 表读取报告产物。"""
    if not db.db_available():
        return []
    try:
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT task_id, service_name, report_type, verdict, risk_score, summary, created_at "
                    "FROM reports ORDER BY created_at DESC LIMIT %s", (limit,))
                rows = cur.fetchall() or []
            return [{
                "type": REPORT,
                "id": r.get("task_id", ""),
                "title": (r.get("summary") or r.get("report_type") or "报告")[:80],
                "status": r.get("verdict", ""),
                "service": r.get("service_name", ""),
                "time": _normalize_time(r.get("created_at")),
                "summary": (r.get("summary") or "")[:120],
                "detail_url": f"/reports?task_id={r.get('task_id', '')}",
            } for r in rows]
        finally:
            conn.close()
    except Exception:
        return []


def list_approvals(limit=50):
    """从 MySQL approval_tasks 表读取审批单产物。"""
    if not db.db_available():
        return []
    try:
        conn = db.get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT task_id, service_name, status, plan, diagnosis, created_at "
                    "FROM approval_tasks ORDER BY created_at DESC LIMIT %s", (limit,))
                rows = cur.fetchall() or []
            return [{
                "type": APPROVAL,
                "id": r.get("task_id", ""),
                "title": ("审批单 " + str(r.get("task_id", ""))[:8]),
                "status": r.get("status", ""),
                "service": r.get("service_name", ""),
                "time": _normalize_time(r.get("created_at")),
                "summary": (r.get("plan") or r.get("diagnosis") or "")[:120],
                "detail_url": f"/approvals?task={r.get('task_id', '')}",
            } for r in rows]
        finally:
            conn.close()
    except Exception:
        return []


def list_flow_runs(limit=50):
    """从 SQLite flow_runs 读取工作流运行产物（遍历所有 flow）。"""
    try:
        from flow_engine.store import FlowStore
        store = FlowStore()
        runs = []
        for flow in store.list_flows():
            for r in store.list_runs(flow["id"]):
                runs.append({
                    "type": FLOW_RUN,
                    "id": r.get("run_id", ""),
                    "title": f"工作流 {flow.get('name', r.get('flow_id', ''))} 运行",
                    "status": r.get("status", ""),
                    "service": "",
                    "time": _normalize_time(r.get("created_at")),
                    "summary": (r.get("error") or "运行完成")[:120],
                    "detail_url": f"/workflows/{flow['id']}/runs/{r.get('run_id', '')}",
                })
        return runs
    except Exception:
        return []


def list_artifacts(limit=50):
    """统一聚合所有产物，按时间倒序，截取 limit 条。"""
    items = list_reports(limit) + list_approvals(limit) + list_flow_runs(limit)
    # 时间倒序（空时间排后）
    items.sort(key=lambda x: x.get("time") or "", reverse=True)
    return items[:limit]


# 类型 → 中文标签（前端展示用）
TYPE_LABELS = {
    REPORT: "报告",
    APPROVAL: "审批",
    FLOW_RUN: "工作流",
}
