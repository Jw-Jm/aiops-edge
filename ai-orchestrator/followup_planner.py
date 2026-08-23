"""P9.6 Follow-up Planner — V9.3 Phase9。

只针对 missing evidence 新增步骤，仍由唯一 Planner 控制并受全局 budget。
follow-up 不能开启第二调查图（§七十五 P9.6）。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Union

FOLLOWUP_STATUSES = {"proposed", "accepted", "rejected", "completed"}


class BudgetExceededError(RuntimeError):
    def __init__(self, message: str):
        self.error_code = "BUDGET_EXCEEDED"
        super().__init__(message)


@dataclass(frozen=True)
class FollowUpRequest:
    """一条 follow-up 补查请求，挂载到唯一调查图（primary）。"""

    followup_id: str
    hypothesis_id: str
    missing_id: str
    tool_id: str
    capability: str
    budget_cost: int
    status: str = "proposed"


class FollowUpPlanner:
    """内存 Follow-up Planner：唯一调查图（primary），受全局 budget 约束。"""

    def __init__(self, max_steps: int, max_tools: int = 10, max_latency: float = 60.0) -> None:
        self.investigation_graph_id = "primary"  # 唯一调查图，不开第二图
        self.max_steps = max_steps
        self.max_tools = max_tools
        self.max_latency = max_latency
        self.consumed_steps = 0
        self.consumed_tools = 0
        self.consumed_latency = 0.0
        self._requests: Dict[str, FollowUpRequest] = {}
        self._seq = 0

    def propose_followup(
        self,
        hypothesis_id: str,
        missing_id: str,
        tool_id: str,
        capability: str,
        budget_cost: int,
    ) -> FollowUpRequest:
        self._seq += 1
        req = FollowUpRequest(
            followup_id=f"fu-{self._seq}",
            hypothesis_id=hypothesis_id,
            missing_id=missing_id,
            tool_id=tool_id,
            capability=capability,
            budget_cost=budget_cost,
            status="proposed",
        )
        self._requests[req.followup_id] = req
        return req

    def _resolve(self, ref: Union[str, FollowUpRequest]) -> str:
        if isinstance(ref, FollowUpRequest):
            return ref.followup_id
        return ref

    def accept(self, ref: Union[str, FollowUpRequest]) -> FollowUpRequest:
        fid = self._resolve(ref)
        req = self._requests.get(fid)
        if req is None:
            raise KeyError(f"followup 不存在: {fid}")
        if req.status != "proposed":
            return req
        # 全局 budget 检查（硬预算）
        if self.consumed_steps + req.budget_cost > self.max_steps:
            raise BudgetExceededError(
                f"follow-up 预算超限: steps={self.consumed_steps}+{req.budget_cost} > max={self.max_steps}"
            )
        self._requests[fid] = _with_status(req, "accepted")
        self.consumed_steps += req.budget_cost
        self.consumed_tools += 1
        return self._requests[fid]

    def reject(self, ref: Union[str, FollowUpRequest]) -> FollowUpRequest:
        fid = self._resolve(ref)
        req = self._requests.get(fid)
        if req is None:
            raise KeyError(f"followup 不存在: {fid}")
        self._requests[fid] = _with_status(req, "rejected")
        return self._requests[fid]

    def complete(self, ref: Union[str, FollowUpRequest]) -> FollowUpRequest:
        fid = self._resolve(ref)
        req = self._requests.get(fid)
        if req is None:
            raise KeyError(f"followup 不存在: {fid}")
        self._requests[fid] = _with_status(req, "completed")
        return self._requests[fid]

    def status(self, ref: Union[str, FollowUpRequest]) -> str:
        fid = self._resolve(ref)
        return self._requests[fid].status

    def all_requests(self) -> List[FollowUpRequest]:
        return list(self._requests.values())


def _with_status(req: FollowUpRequest, status: str) -> FollowUpRequest:
    from dataclasses import replace

    return replace(req, status=status)
