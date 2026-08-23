"""EX.4 Rollback Framework — V9.3 Execution Infrastructure 回滚机制。

核心原则（EX.4 + 评审补充）：
- Rollback 责任边界：Agent 建议 / Human 批准 / Policy 检查 / Adapter 执行；禁 Agent 自动 rollback。
- rollback_contract_id：Rollback 是新动作，不复用原 contract（审计清晰）。
- 回滚目标 = before_state（Adapter 采集的执行前状态）；无 before_state → 拒绝。
- 无 Human 批准 → 拒绝；rollback 经 Policy 检查。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Any, Dict, Optional

from execution_contract import ExecutionContract

ROLLBACK_STATUSES = {"pending_approval", "approved", "executed", "rejected"}


class RollbackDenied(ValueError):
    def __init__(self, message: str):
        self.error_code = "ROLLBACK_DENIED"
        super().__init__(message)


@dataclass
class RollbackRequest:
    request_id: str
    rollback_contract_id: str
    original_contract_id: str
    before_state: Dict[str, Any]
    requested_by: str
    approved_by: str = ""
    rollback_action: str = "rollback"
    status: str = "pending_approval"

    def __post_init__(self) -> None:
        if self.status not in ROLLBACK_STATUSES:
            raise ValueError(f"非法 status: {self.status}")


class RollbackFramework:
    """内存 Rollback Framework（MVP）。"""

    def __init__(self) -> None:
        self._store: Dict[str, RollbackRequest] = {}

    def request_rollback(
        self,
        *,
        original_contract: ExecutionContract,
        before_state: Optional[Dict[str, Any]],
        requested_by: str,
    ) -> RollbackRequest:
        """生成 Rollback 请求（新 rollback_contract_id）。无 before_state → 拒绝。"""
        if not before_state:
            raise RollbackDenied("无 before_state，无法确定回滚目标")
        req = RollbackRequest(
            request_id=str(uuid.uuid4()),
            rollback_contract_id=str(uuid.uuid4()),  # Rollback 是新动作，不复用原 contract
            original_contract_id=original_contract.contract_id,
            before_state=dict(before_state),
            requested_by=requested_by,
            status="pending_approval",
        )
        self._store[req.request_id] = req
        return req

    def approve(self, request_id: str, *, approved_by: str) -> RollbackRequest:
        """Human 批准。"""
        req = self._store.get(request_id)
        if req is None:
            raise RollbackDenied(f"rollback 请求不存在: {request_id}")
        req.approved_by = approved_by
        req.status = "approved"
        return req

    def execute_rollback(self, request_id: str, *, policy_allows: bool = True) -> bool:
        """执行回滚（经 Policy 检查 + Human 批准）。"""
        req = self._store.get(request_id)
        if req is None:
            raise RollbackDenied(f"rollback 请求不存在: {request_id}")
        if req.status != "approved":
            raise RollbackDenied("rollback 未获 Human 批准，不能执行（Agent 不能自动 rollback）")
        if not policy_allows:
            req.status = "rejected"
            raise RollbackDenied("rollback 被 Policy 拒绝")
        req.status = "executed"
        return True
