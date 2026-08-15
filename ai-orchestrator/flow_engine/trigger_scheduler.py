# flow_engine/trigger_scheduler.py
"""cron 触发器: 30s 扫描启用的 workflow, 与 APScheduler job 幂等对齐"""
import logging
from datetime import datetime, timezone

from apscheduler.triggers.cron import CronTrigger

log = logging.getLogger(__name__)
SCAN_INTERVAL_SECONDS = 30


def _now_utc() -> str:
    return datetime.now(timezone.utc).isoformat()


class CronTriggerManager:
    def __init__(self, scheduler, list_enabled_flows, run_flow):
        self._sched = scheduler
        self._list = list_enabled_flows
        self._run = run_flow
        self._jobs = {}  # flow_id -> (job_id, cron_expr)

    def sync(self):
        desired = {}
        for f in self._list():
            for node in (f.get("graph") or {}).get("nodes", []):
                if node.get("type") == "trigger.cron":
                    desired[f["id"]] = (node.get("config") or {}).get("cron") or "0 * * * *"
        for flow_id, cron_expr in desired.items():
            if flow_id in self._jobs and self._jobs[flow_id][1] == cron_expr:
                continue
            if flow_id in self._jobs:
                try:
                    self._sched.remove_job(self._jobs[flow_id][0])
                except Exception:
                    pass
            job = self._sched.add_job(
                self._job_for(flow_id), CronTrigger.from_crontab(cron_expr), args=[flow_id],
                id=f"flow-cron-{flow_id}", replace_existing=True, misfire_grace_time=60)
            self._jobs[flow_id] = (job.id, cron_expr)
        for flow_id in list(self._jobs):
            if flow_id not in desired:
                try:
                    self._sched.remove_job(self._jobs.pop(flow_id)[0])
                except Exception:
                    pass

    def _job_for(self, flow_id: str):
        """零参闭包: 兼容 APScheduler(调用 fn(*args)) 与测试直接 fn() 两种触发方式。"""
        def _job(*_args, **_kwargs):
            self._fire(flow_id)
        return _job

    def _fire(self, flow_id: str):
        log.info("cron 触发 workflow %s", flow_id)
        self._run(flow_id, trigger={"type": "cron", "fired_at": _now_utc(), "payload": {}})
