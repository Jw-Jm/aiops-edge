"""Security boundaries for the production investigation worker."""

import os
import subprocess
import sys

import pytest


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


@pytest.mark.asyncio
async def test_worker_lifespan_starts_canonical_graph_reconcile(monkeypatch):
    """The stateless worker must own the same source-reconcile lifecycle as the gateway."""
    import apps.investigation as worker

    class Dispatcher:
        async def stop(self):
            return None

    class Runtime:
        def __init__(self):
            self.started = 0
            self.stopped = 0

        def start(self):
            self.started += 1

        async def stop(self):
            self.stopped += 1

    runtime = Runtime()
    dispatcher = Dispatcher()
    monkeypatch.setenv("GRAPH_BACKEND", "hugegraph")
    monkeypatch.setenv("GRAPH_SOURCE_RECONCILE_ENABLED", "1")
    monkeypatch.delenv("QUERY_API_URL", raising=False)
    monkeypatch.setattr(worker, "_dispatcher", dispatcher)
    monkeypatch.setattr(worker, "_recovery_task", None)

    import kg.runtime as graph_runtime
    monkeypatch.setattr(graph_runtime, "build_graph_sync_runtime", lambda: runtime)

    async with worker.lifespan(None):
        assert runtime.started == 1
    assert runtime.stopped == 1
