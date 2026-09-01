"""Security boundaries for the production investigation worker."""

import os
import subprocess
import sys


def test_public_probe_paths_are_exact_boundaries():
    # Importing apps.investigation sets process-wide worker mode by design;
    # keep that composition-root side effect out of the pytest process.
    env = os.environ.copy()
    env.update({
        "INVESTIGATION_WORKER_MODE": "true",
        "LLM_MOCK": "true",
        "AIOPS_DEPLOYMENT_MODE": "development",
    })
    code = (
        "from apps.investigation import _public_path_allowed as worker; "
        "from main import _public_auth_path_allowed as gateway; "
        "assert worker('/health') and gateway('/health'); "
        "assert worker('/readyz') and gateway('/readyz'); "
        "assert worker('/metrics') and gateway('/metrics'); "
        "assert not worker('/healthz') and not gateway('/healthz'); "
        "assert not worker('/health/extra') and not gateway('/health/extra'); "
        "assert not worker('/readyzmalicious') and not gateway('/readyzmalicious'); "
        "assert not worker('/metrics/debug') and not gateway('/metrics/debug')"
    )
    result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True,
                            env=env, cwd=os.path.dirname(os.path.dirname(__file__)), timeout=30)
    assert result.returncode == 0, result.stderr
