"""P9.5 Missing Evidence Engine — V9.3 Phase9。

每条 hypothesis 明确 critical/optional missing。critical missing 会限制最终状态，
不得通过语言润色掩盖（§七十五 P9.5）。
reason 复用 claim_type=unknown 冻结枚举（§三十四）。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, List

MISSING_REASONS = {
    "insufficient_data",
    "permission_denied",
    "unavailable_source",
    "expired_evidence",
}


class MissingEvidenceError(ValueError):
    def __init__(self, message: str):
        self.error_code = "MISSING_EVIDENCE_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class MissingEvidence:
    """一条缺失证据。critical=true 会限制最终状态。"""

    missing_id: str
    hypothesis_id: str
    required_type: str
    critical: bool
    reason: str
    followup_slot: str = ""


class MissingEvidenceEngine:
    """内存 Missing Evidence Engine：跟踪每条 hypothesis 的缺失证据。"""

    def __init__(self) -> None:
        self._missing: Dict[str, List[MissingEvidence]] = {}
        self._seq = 0

    def add_missing(
        self,
        hypothesis_id: str,
        required_type: str,
        critical: bool,
        reason: str,
        followup_slot: str = "",
    ) -> MissingEvidence:
        if reason not in MISSING_REASONS:
            raise MissingEvidenceError(f"非法 missing reason: {reason}")
        self._seq += 1
        m = MissingEvidence(
            missing_id=f"ms-{self._seq}",
            hypothesis_id=hypothesis_id,
            required_type=required_type,
            critical=critical,
            reason=reason,
            followup_slot=followup_slot,
        )
        self._missing.setdefault(hypothesis_id, []).append(m)
        return m

    def derive(self, hypothesis: Any, evidences: List[Any]) -> List[MissingEvidence]:
        """从 Hypothesis.required_support vs 实际 Evidence 类型主动推导缺失类别（§七十五 P9.5）。

        - 对 hypothesis.required_support 中每个 evidence_type，若无对应 Evidence 类型 → 缺失。
        - required support 缺失视为 critical（限制最终状态）。
        - reason = insufficient_data（数据不足）。
        """
        hid = hypothesis.hypothesis_id
        present_types = {getattr(ev, "evidence_type", "") for ev in evidences}
        derived: List[MissingEvidence] = []
        for required in getattr(hypothesis, "required_support", []) or []:
            if required in present_types:
                continue
            m = self.add_missing(
                hypothesis_id=hid,
                required_type=required,
                critical=True,
                reason="insufficient_data",
            )
            derived.append(m)
        return derived

    def critical_missing(self, hypothesis_id: str) -> List[MissingEvidence]:
        return [m for m in self._missing.get(hypothesis_id, []) if m.critical]

    def optional_missing(self, hypothesis_id: str) -> List[MissingEvidence]:
        return [m for m in self._missing.get(hypothesis_id, []) if not m.critical]

    def all_missing(self, hypothesis_id: str) -> List[MissingEvidence]:
        return list(self._missing.get(hypothesis_id, []))

    def has_critical_missing(self, hypothesis_id: str) -> bool:
        return bool(self.critical_missing(hypothesis_id))

    def blocks_confirmation(self, hypothesis_id: str) -> bool:
        """存在 critical missing → 限制最终状态（无法 confirmed / 自动补救）。"""
        return self.has_critical_missing(hypothesis_id)
