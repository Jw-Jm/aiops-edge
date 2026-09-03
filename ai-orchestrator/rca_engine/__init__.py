"""Canonical RCA engine package.

P1-A1：legacy feature-flag 兼容桥已删除。Phase 9 单一 RCA 编排器（RcaEngine）
由 `rca_engine.phase9_engine` 提供并**静态导出**——不再存在运行时 legacy
fallback / 环境开关选择新旧实现。生产与本地装配同一个实现。

公共符号：
- RcaEngine / EvidenceScopeMismatch / RcaComputation / HypothesisEvaluation
  （V9.3 Phase 9 canonical 编排器，来自 phase9_engine）
- RCARequest / RCAResult / diagnose_root_cause_v2（诊断封装，来自 engine）
"""
from __future__ import annotations

from .engine import RCARequest, RCAResult, diagnose_root_cause_v2
from .phase9_engine import (
    EvidenceScopeMismatch,
    HypothesisEvaluation,
    RcaComputation,
    RcaEngine,
)

__all__ = [
    "RcaEngine",
    "EvidenceScopeMismatch",
    "RcaComputation",
    "HypothesisEvaluation",
    "RCARequest",
    "RCAResult",
    "diagnose_root_cause_v2",
]
