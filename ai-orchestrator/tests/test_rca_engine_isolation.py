"""Production RCA package must not import the retired compatibility engine."""

from __future__ import annotations

import os
import subprocess
import sys


def test_production_import_does_not_load_legacy_engine():
    code = (
        "import sys; import rca_engine; "
        "assert '_aiops_rca_engine_legacy' not in sys.modules; "
        "assert not hasattr(rca_engine, 'RcaEngine'); "
        "assert rca_engine.RCARequest"
    )
    env = os.environ.copy()
    env["AIOPS_ENV"] = "production"
    env.pop("AIOPS_DEPLOYMENT_MODE", None)
    env.pop("AIOPS_LEGACY_RCA_COMPAT", None)
    env.pop("AIOPS_ALLOW_LEGACY_COMPAT_IN_PRODUCTION", None)
    subprocess.run([sys.executable, "-c", code], check=True, env=env)


def test_conflicting_production_mode_markers_still_fail_closed():
    code = (
        "import sys; import rca_engine; "
        "assert '_aiops_rca_engine_legacy' not in sys.modules; "
        "assert not hasattr(rca_engine, 'RcaEngine')"
    )
    env = os.environ.copy()
    env["AIOPS_ENV"] = "development"
    env["AIOPS_DEPLOYMENT_MODE"] = "production"
    env.pop("AIOPS_LEGACY_RCA_COMPAT", None)
    env.pop("AIOPS_ALLOW_LEGACY_COMPAT_IN_PRODUCTION", None)
    subprocess.run([sys.executable, "-c", code], check=True, env=env)


def test_local_import_retains_migration_compatibility():
    code = (
        "import os; os.environ.pop('AIOPS_ENV', None); "
        "os.environ.pop('AIOPS_DEPLOYMENT_MODE', None); "
        "from rca_engine import RcaEngine; assert RcaEngine"
    )
    subprocess.run([sys.executable, "-c", code], check=True, env=os.environ.copy())
