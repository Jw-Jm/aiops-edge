"""Versioned RCA engine package with an explicit legacy compatibility bridge."""
from __future__ import annotations

import importlib.util
import sys
from pathlib import Path


def _load_legacy():
    path = Path(__file__).resolve().parent.parent / "rca_engine_legacy.py"
    spec = importlib.util.spec_from_file_location("_aiops_rca_engine_legacy", path)
    if spec is None or spec.loader is None:
        raise ImportError(f"legacy RCA module unavailable: {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


_legacy = _load_legacy()
RcaEngine = _legacy.RcaEngine
EvidenceScopeMismatch = _legacy.EvidenceScopeMismatch
RcaComputation = _legacy.RcaComputation
HypothesisEvaluation = _legacy.HypothesisEvaluation

from .engine import RCARequest, RCAResult, diagnose_root_cause_v2  # noqa: E402

__all__ = ["RcaEngine", "EvidenceScopeMismatch", "RcaComputation", "HypothesisEvaluation",
           "RCARequest", "RCAResult", "diagnose_root_cause_v2"]
