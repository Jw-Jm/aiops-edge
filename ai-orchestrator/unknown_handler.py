"""P9.10 Unknown-safe Handler — V9.3 Phase9。

无法达到阈值或 critical evidence 缺失时（§七十五 P9.10）：
  root_cause = unknown
  missing_evidence = explicit
  no automatic remediation

F5：P9 不触发任何执行动作 / Structured OpsAction（属 Phase 11）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import List, Optional


@dataclass(frozen=True)
class UnknownSafeResult:
    run_id: str
    root_cause: str
    missing_evidence: List[str] = field(default_factory=list)
    automatic_remediation: bool = False
    ops_actions: List[str] = field(default_factory=list)

    @property
    def explicit_missing(self) -> bool:
        return bool(self.missing_evidence)

    @property
    def is_unknown(self) -> bool:
        return self.root_cause == "unknown"


class UnknownSafeHandler:
    """Unknown-safe 行为：无法判定时显式 unknown，不触发自动补救。"""

    def handle(
        self,
        run_id: str,
        root_cause: Optional[str],
        missing_evidence: List[str],
    ) -> UnknownSafeResult:
        rc = root_cause if root_cause else "unknown"
        # P9 永不产生自动补救 / 执行动作（F5）
        return UnknownSafeResult(
            run_id=run_id,
            root_cause=rc,
            missing_evidence=list(missing_evidence),
            automatic_remediation=False,
            ops_actions=[],
        )
