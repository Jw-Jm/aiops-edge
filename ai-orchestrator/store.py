"""Shared task store — 内存视图 + MySQL 持久化同步。

设计：`_task_store` 保持内存 dict 语义（main.py 现有读写逻辑不变），
在"整个任务写入"(dict[key] = task) 时同步到 MySQL ApprovalStore。
字段级修改（dict[key][field]=v）由调用方在关键节点（审批/驳回/诊断完成）显式持久化。
MySQL 不可用时静默降级为纯内存。
"""
from db_approval import ApprovalStore


class _TaskStore(dict):
    """dict 子类：整个任务写入时同步持久化到 MySQL。"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._approval = ApprovalStore()

    def __setitem__(self, key, value):
        existed = key in self
        super().__setitem__(key, value)
        try:
            if existed:
                self._approval.update(key, status=value.get("status", ""),
                                      plan=value.get("plan", ""), script=value.get("script", ""),
                                      risk_score=float(value.get("risk_score", 0) or 0),
                                      risk_reason=value.get("risk_reason", ""),
                                      diagnosis=value.get("diagnosis", ""),
                                      report=value.get("report", ""),
                                      decided_at=value.get("done_at") or None)
            else:
                self._approval.create(value)
        except Exception:
            pass

    def persist(self, key):
        """显式将某个任务的最新状态同步到 MySQL（用于字段级修改后落库）。"""
        try:
            if key in self:
                value = self[key]
                self._approval.update(key, status=value.get("status", ""),
                                      plan=value.get("plan", ""), script=value.get("script", ""),
                                      risk_score=float(value.get("risk_score", 0) or 0),
                                      risk_reason=value.get("risk_reason", ""),
                                      diagnosis=value.get("diagnosis", ""),
                                      report=value.get("report", ""),
                                      decided_at=value.get("done_at") or None)
        except Exception:
            pass

    def setdefault(self, key, default=None):
        if key in self:
            return self[key]
        self[key] = default
        return default

    def update(self, *args, **kwargs):
        for k, v in dict(*args, **kwargs).items():
            self[k] = v


_task_store: dict = _TaskStore()

