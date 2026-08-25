"""Investigation worker must not initialize the legacy SQLite session store."""

from __future__ import annotations

import os
import subprocess
import sys


def test_investigation_worker_never_initializes_sqlite():
    env = os.environ.copy()
    env.update({
        "INVESTIGATION_WORKER_MODE": "true",
        "LLM_MOCK": "true",
        "AIOPS_DEPLOYMENT_MODE": "development",
    })
    code = (
        "from orchestrator import brain; "
        "assert brain._stateless_worker is True; "
        "assert brain._conn is None; "
        "assert brain._db_path == ''; "
        "print('stateless-ok')"
    )
    result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, env=env,
                            cwd=os.path.dirname(os.path.dirname(__file__)), timeout=30)
    assert result.returncode == 0, result.stderr
    assert "stateless-ok" in result.stdout
