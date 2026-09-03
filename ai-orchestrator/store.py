"""Shared task store — 内存视图 + MySQL 持久化同步。

设计：`_task_store` 保持内存 dict 语义（main.py 现有读写逻辑不变），
在"整个任务写入"(dict[key] = task) 时同步到 MySQL ApprovalStore。
字段级修改（dict[key][field]=v）由调用方在关键节点（审批/驳回/诊断完成）显式持久化。

P1-R1 fail-closed：显式持久化（persist / decide / approve）在 MySQL 不可用或写
失败时抛 ApprovalStoreError（由调用方返回 5xx），不得把内存当持久授权。
自动同步（__setitem__）写失败只做日志 + degraded 标记（任务内存视图不被 DB
故障阻断；读路径看到 degraded 时 UI 明确提示非持久）。

内存保护：`_task_store` 有容量上限（MAX_TASKS），超过时丢弃最旧任务，
防止长期运行内存无限增长（MySQL 已持久化，丢弃仅影响内存视图）。
"""
import logging

from db_approval import ApprovalStore, ApprovalStoreError

logger = logging.getLogger("aiops.store")

# 内存任务表上限（MySQL 持久化不受影响，仅内存视图裁剪）
MAX_TASKS = 5000


class _TaskStore(dict):
    """dict 子类：整个任务写入时同步持久化到 MySQL。"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._approval = ApprovalStore()

    def __setitem__(self, key, value):
        existed = key in self
        super().__setitem__(key, value)
        # 内存保护：超过上限丢弃最旧任务（dict 保持插入顺序，首项最旧）
        if not existed and len(self) > MAX_TASKS:
            try:
                oldest = next(iter(self))
                super().__delitem__(oldest)
            except Exception:  # noqa: S110, BLE001 — MAX_TASKS 内存裁剪为 best-effort
                pass
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
        except ApprovalStoreError:
            logger.exception("task store auto-sync failed (degraded, memory-only view) key=%s", key)

    def persist(self, key):
        """显式将某个任务的最新状态同步到 MySQL（用于字段级修改后落库）。

        P1-R1: 审批状态持久化失败必须向调用方传播（fail-closed），
        不得把"内存成功"当作审批授权依据。调用方（如 approve_task）需处理异常返回 5xx。
        """
        if key in self:
            value = self[key]
            self._approval.update(key, status=value.get("status", ""),
                                  plan=value.get("plan", ""), script=value.get("script", ""),
                                  risk_score=float(value.get("risk_score", 0) or 0),
                                  risk_reason=value.get("risk_reason", ""),
                                  diagnosis=value.get("diagnosis", ""),
                                  report=value.get("report", ""),
                                  decided_at=value.get("done_at") or None)

    def setdefault(self, key, default=None):
        if key in self:
            return self[key]
        self[key] = default
        return default

    def update(self, *args, **kwargs):
        for k, v in dict(*args, **kwargs).items():
            self[k] = v


_task_store: dict = _TaskStore()

