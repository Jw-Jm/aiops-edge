"""Production composition root must not import legacy runtime owners."""

from __future__ import annotations

import os
import subprocess
import sys


def test_production_import_does_not_construct_legacy_runtime_owners():
    env = os.environ.copy()
    env.update(
        {
            "AIOPS_ENV": "production",
            "AIOPS_DEPLOYMENT_MODE": "production",
            "LLM_MOCK": "false",
            "QUERY_API_URL": "http://query-api.invalid:8080/api/v1",
            "TRUSTED_CONTEXT_PRIVATE_KEY": "test-private-key",
            "INTERNAL_TOKEN": "test-internal-token",
            "AIOPS_DATA_DIR": "/tmp/aiops-production-import-boundary",
        }
    )
    code = """
import run_store_factory
run_store_factory.check_control_plane_reachable = lambda *args, **kwargs: None
import main
assert main.scheduler is None
assert main.shell_policy is None
assert main.flow_router is None
assert main.kg_router is None
assert main.agent_tool is None
print('production-import-boundary-ok')
"""
    result = subprocess.run(
        [sys.executable, "-c", code],
        cwd=os.path.dirname(os.path.dirname(__file__)),
        env=env,
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert result.returncode == 0, result.stderr
    assert "production-import-boundary-ok" in result.stdout
