"""P7.7 Structured Investigation State — V9.3 Phase7 围绕 Run 维护调查状态。

核心原则：
- 结构化状态，不靠 prompt 历史恢复（P7.7 禁止）。
- findings/hypotheses 只记录调查结果，不含执行指令。
- 状态更新不影响外部资源。
- 禁止新增 Incident/Detection 状态机。
- 状态机与 P7.5 一致：IntentCreated → EvidenceCollected → PlanGenerated → AwaitingApproval（Phase7 停此）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

import contracts

PHASES = {"IntentCreated", "EvidenceCollected", "PlanGenerated", "AwaitingApproval"}
_STEP_STATUSES = {"pending", "running", "completed", "skipped", "failed"}
_VALID_TRANSITIONS = {
    "IntentCreated": {"EvidenceCollected"},
    "EvidenceCollected": {"PlanGenerated"},
    "PlanGenerated": {"AwaitingApproval"},
    "AwaitingApproval": set(),
}

# 执行指令关键词（findings/hypotheses 中禁止）
_EXECUTION_KEYWORDS = ("kubectl", "delete ", "exec ", "apply ", "curl http", "restart")


@dataclass
class StepState:
    step_id: str
    status: str = "pending"
    result_ref: Optional[str] = None
    started_at: Optional[str] = None
    finished_at: Optional[str] = None


@dataclass
class InvestigationState:
    run_id: str
    intent_id: str
    plan_id: str
    phase: str = "IntentCreated"
    steps: List[StepState] = field(default_factory=list)
    tool_result_refs: List[str] = field(default_factory=list)
    evidence_refs: List[str] = field(default_factory=list)
    missing_evidence: List[Dict[str, Any]] = field(default_factory=list)
    budget: Dict[str, Any] = field(
        default_factory=lambda: {
            "consumed_steps": 0, "consumed_tools": 0, "consumed_latency": 0, "remaining": 0,
        }
    )
    findings: List[str] = field(default_factory=list)
    # R2 收敛：hypotheses 组合权威 contracts.Hypothesis（消除同名平行模型，引用不复制）
    hypotheses: List[contracts.Hypothesis] = field(default_factory=list)
    confidence: float = 0.0
    status: str = "IntentCreated"

    def __post_init__(self) -> None:
        if self.phase not in PHASES:
            raise ValueError(f"非法 phase: {self.phase}")

    def to_dict(self) -> Dict[str, Any]:
        return {
            "run_id": self.run_id,
            "intent_id": self.intent_id,
            "plan_id": self.plan_id,
            "phase": self.phase,
            "steps": [
                {
                    "step_id": s.step_id, "status": s.status, "result_ref": s.result_ref,
                    "started_at": s.started_at, "finished_at": s.finished_at,
                }
                for s in self.steps
            ],
            "tool_result_refs": list(self.tool_result_refs),
            "evidence_refs": list(self.evidence_refs),
            "missing_evidence": [dict(m) for m in self.missing_evidence],
            "budget": dict(self.budget),
            "findings": list(self.findings),
            "hypotheses": [h.model_dump() for h in self.hypotheses],
            "confidence": self.confidence,
            "status": self.status,
        }


class StateStore:
    """内存结构化状态 Store（MVP）。真实持久化属后续阶段。"""

    def __init__(self) -> None:
        self._store: Dict[str, InvestigationState] = {}

    def create(
        self, *, run_id: str, intent_id: str, plan_id: str, max_steps: int = 10
    ) -> InvestigationState:
        st = InvestigationState(
            run_id=run_id,
            intent_id=intent_id,
            plan_id=plan_id,
            phase="IntentCreated",
            budget={
                "consumed_steps": 0, "consumed_tools": 0, "consumed_latency": 0, "remaining": max_steps,
            },
            status="IntentCreated",
        )
        self._store[run_id] = st
        return st

    def get(self, run_id: str) -> InvestigationState:
        st = self._store.get(run_id)
        if st is None:
            raise KeyError(f"run 状态不存在: {run_id}")
        return st

    def restore(self, snapshot: Dict[str, Any]) -> InvestigationState:
        """从结构化快照恢复（非 prompt 历史）。"""
        st = InvestigationState(
            run_id=snapshot["run_id"],
            intent_id=snapshot["intent_id"],
            plan_id=snapshot["plan_id"],
            phase=snapshot["phase"],
            steps=[StepState(**s) for s in snapshot.get("steps", [])],
            tool_result_refs=list(snapshot.get("tool_result_refs", [])),
            evidence_refs=list(snapshot.get("evidence_refs", [])),
            missing_evidence=[dict(m) for m in snapshot.get("missing_evidence", [])],
            budget=dict(snapshot.get("budget", {})),
            findings=list(snapshot.get("findings", [])),
            hypotheses=[
                contracts.Hypothesis.model_validate(h)
                for h in snapshot.get("hypotheses", [])
            ],
            confidence=snapshot.get("confidence", 0.0),
            status=snapshot.get("status", snapshot["phase"]),
        )
        self._store[st.run_id] = st
        return st

    def add_step(self, run_id: str, step: StepState) -> InvestigationState:
        st = self.get(run_id)
        st.steps.append(step)
        return st

    def update_step(self, run_id: str, step_id: str, status: str, result_ref: Optional[str] = None) -> InvestigationState:
        st = self.get(run_id)
        if status not in _STEP_STATUSES:
            raise ValueError(f"非法 step status: {status}")
        for s in st.steps:
            if s.step_id == step_id:
                s.status = status
                if result_ref is not None:
                    s.result_ref = result_ref
        return st

    def add_missing_evidence(self, run_id: str, slot: Dict[str, Any]) -> InvestigationState:
        st = self.get(run_id)
        st.missing_evidence.append(dict(slot))
        return st

    def consume_budget(self, run_id: str, *, steps: int = 0, tools: int = 0) -> InvestigationState:
        st = self.get(run_id)
        new_steps = st.budget["consumed_steps"] + steps
        max_steps = st.budget["consumed_steps"] + st.budget["remaining"]
        if new_steps > max_steps:
            raise ValueError(f"预算超限: consumed_steps={new_steps} > max={max_steps}")
        st.budget["consumed_steps"] = new_steps
        st.budget["consumed_tools"] = st.budget["consumed_tools"] + tools
        st.budget["remaining"] = max_steps - new_steps
        return st

    def add_finding(self, run_id: str, text: str) -> InvestigationState:
        st = self.get(run_id)
        lowered = text.lower()
        for kw in _EXECUTION_KEYWORDS:
            if kw in lowered:
                raise ValueError(f"findings 禁止含执行指令: {kw}")
        st.findings.append(text)
        return st

    def add_hypothesis(self, run_id: str, hypothesis: contracts.Hypothesis) -> InvestigationState:
        st = self.get(run_id)
        lowered = (hypothesis.description or hypothesis.title).lower()
        for kw in _EXECUTION_KEYWORDS:
            if kw in lowered:
                raise ValueError(f"hypothesis 禁止含执行指令: {kw}")
        st.hypotheses.append(hypothesis)
        return st

    def transition(self, run_id: str, new_phase: str) -> InvestigationState:
        if new_phase not in PHASES:
            raise ValueError(f"非法 phase（Phase7 不进入 {new_phase}）: {new_phase}")
        st = self.get(run_id)
        allowed = _VALID_TRANSITIONS.get(st.phase, set())
        if new_phase not in allowed:
            raise ValueError(f"非法迁移: {st.phase} → {new_phase}")
        st.phase = new_phase
        st.status = new_phase
        return st
