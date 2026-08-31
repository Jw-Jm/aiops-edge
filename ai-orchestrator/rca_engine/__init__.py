"""Versioned RCA engine package with an explicit legacy compatibility bridge.

The compatibility engine is available to local migration tests only. A
production Gateway/Investigation Worker must load RCA V2 exclusively so an
old scorer cannot become a second root-cause owner through an import side
effect. The Docker build also excludes the compatibility module entirely.
"""
from __future__ import annotations

import importlib.util
import os
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


def _legacy_compat_enabled() -> bool:
    """Return whether the retired RCA bridge may be loaded.

    Local tests/migrations retain an explicit compatibility path, but
    production is fail-closed even if a stale environment flag is present.
    This keeps the V2 package import free of legacy code in the real image.
    """
    modes = {
        os.environ.get("AIOPS_ENV", "").strip().lower(),
        os.environ.get("AIOPS_DEPLOYMENT_MODE", "").strip().lower(),
    }
    if "production" in modes:
        return os.environ.get("AIOPS_LEGACY_RCA_COMPAT", "0").strip().lower() in {
            "1", "true", "yes", "on"
        } and os.environ.get("AIOPS_ALLOW_LEGACY_COMPAT_IN_PRODUCTION", "0").strip().lower() in {
            "1", "true", "yes", "on"
        }
    return True


_legacy = _load_legacy() if _legacy_compat_enabled() else None
if _legacy is not None:
    RcaEngine = _legacy.RcaEngine
    EvidenceScopeMismatch = _legacy.EvidenceScopeMismatch
    RcaComputation = _legacy.RcaComputation
    HypothesisEvaluation = _legacy.HypothesisEvaluation

from .engine import RCARequest, RCAResult, diagnose_root_cause_v2  # noqa: E402

__all__ = ["RCARequest", "RCAResult", "diagnose_root_cause_v2"]
if _legacy is not None:
    __all__ = ["RcaEngine", "EvidenceScopeMismatch", "RcaComputation", "HypothesisEvaluation", *__all__]
