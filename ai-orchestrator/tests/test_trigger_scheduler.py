# tests/test_trigger_scheduler.py
from flow_engine.trigger_scheduler import CronTriggerManager


class FakeSched:
    def __init__(self): self.jobs = {}
    def add_job(self, fn, trigger, args, id, replace_existing, misfire_grace_time):
        self.jobs[id] = (fn, args); return type("J", (), {"id": id})()
    def remove_job(self, job_id): self.jobs.pop(job_id, None)


def test_sync_adds_and_removes_cron_jobs():
    flows = [
        {"id": "f1", "graph": {"nodes": [{"type": "trigger.cron", "config": {"cron": "0 * * * *"}}]}},
        {"id": "f2", "graph": {"nodes": [{"type": "trigger.manual"}]}},
    ]
    ran = []
    mgr = CronTriggerManager(FakeSched(), lambda: flows, lambda fid, trigger: ran.append(fid))
    mgr.sync()
    assert "flow-cron-f1" in mgr._sched.jobs          # 有 cron 的加 job
    mgr._sched.jobs["flow-cron-f1"][0]()             # 触发
    assert ran == ["f1"]
    flows.pop(0)                                      # f1 删除
    mgr.sync()
    assert "flow-cron-f1" not in mgr._sched.jobs      # job 同步移除
