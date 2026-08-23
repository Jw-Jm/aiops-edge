"""R1 激活基线 — Activation Record + checksum manifest（审计阻断项 B0-01）。

审计要求：必须有正式 V9.3 Activation Record（P7 Entry Criteria 未满足前不得标记
后续 Phase 完成），且不能只依赖可变工作目录或累计测试数字 → 需代码/文档 checksum manifest。

本模块提供：
- ActivationRecord：记录 phase 激活状态（phase_id/status/gate/entry_criteria_met/activated_at）。
- ActivationLedger：正式激活账本，禁止跳过前置 phase / 未满足 entry criteria 就激活（fail-closed）。
- ChecksumManifest：对文件生成 SHA256 manifest，verify() 检测漂移（运行期文件被篡改）。

与既有冻结合同一致：真实执行仍 NOT YET APPROVED，本模块仅记录激活状态，不触发任何执行。
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from datetime import datetime, timezone
from enum import Enum
from typing import Dict, List, Optional

# 合法的 Phase 顺序（后续 Phase 追加）
PHASE_ORDER = ["P7", "P8", "P9", "P10", "P11"]


class PhaseStatus(str, Enum):
    NOT_ACTIVATED = "not_activated"
    ACTIVE = "active"
    COMPLETE = "complete"


class ActivationError(ValueError):
    def __init__(self, message: str):
        self.error_code = "ACTIVATION_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class ActivationRecord:
    """一个 Phase 的正式激活记录。"""

    phase_id: str
    status: PhaseStatus
    gate: str
    entry_criteria_met: bool
    activated_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))


class ActivationLedger:
    """正式激活账本：禁止跳过前置 phase / 未满足 entry criteria 激活（fail-closed）。"""

    def __init__(self) -> None:
        self._records: Dict[str, ActivationRecord] = {}

    def activate(
        self,
        phase_id: str,
        gate: str,
        entry_criteria_met: bool,
    ) -> ActivationRecord:
        if phase_id not in PHASE_ORDER:
            raise ActivationError(f"未知 phase: {phase_id}")
        # Entry Criteria 门控：未满足 → 拒绝激活（fail-closed）
        if not entry_criteria_met:
            raise ActivationError(
                f"Phase {phase_id} entry criteria 未满足，禁止激活"
            )
        # 前置 phase 必须已激活（跳过 → 拒绝）
        idx = PHASE_ORDER.index(phase_id)
        for pred in PHASE_ORDER[:idx]:
            pr = self._records.get(pred)
            if pr is None:
                raise ActivationError(
                    f"Phase {phase_id} 的前置 {pred} 未激活（禁止跳级）"
                )
        rec = ActivationRecord(
            phase_id=phase_id,
            status=PhaseStatus.ACTIVE,
            gate=gate,
            entry_criteria_met=True,
        )
        self._records[phase_id] = rec
        return rec

    def status(self, phase_id: str) -> Optional[PhaseStatus]:
        rec = self._records.get(phase_id)
        return rec.status if rec else None

    def records(self) -> List[ActivationRecord]:
        return list(self._records.values())


class ChecksumManifest:
    """对文件生成 SHA256 manifest，verify() 检测漂移（运行期文件被篡改）。"""

    def __init__(self, entries: Dict[str, str]) -> None:
        self.entries = dict(entries)

    @classmethod
    def build(cls, file_paths: List[str]) -> "ChecksumManifest":
        entries: Dict[str, str] = {}
        for path in file_paths:
            with open(path, "rb") as f:
                entries[path] = hashlib.sha256(f.read()).hexdigest()
        return cls(entries)

    def verify(self) -> bool:
        """验证 manifest 中每个文件的当前 checksum 与记录的 checksum 一致。"""
        for path, expected in self.entries.items():
            try:
                with open(path, "rb") as f:
                    current = hashlib.sha256(f.read()).hexdigest()
            except FileNotFoundError:
                return False
            if current != expected:
                return False
        return True
