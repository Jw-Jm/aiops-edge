"""Production Gateway route inventory is fail-closed."""

import os
from pathlib import Path
import subprocess
import sys

from fastapi import APIRouter, FastAPI
from fastapi.testclient import TestClient

from production_surface import filter_production_routes, route_is_production_allowed


class _Route:
    def __init__(self, path: str, methods=None):
        self.path = path
        self.methods = methods


def test_production_surface_keeps_only_signed_internal_and_probe_routes():
    routes = [
        _Route("/health", {"GET"}),
        _Route("/readyz", {"GET"}),
        _Route("/metrics", {"GET"}),
        _Route("/internal/v1/run-invocations", {"POST"}),
        _Route("/internal/v1/chat", {"POST"}),
        _Route("/internal/v1/run-controls/{operation}", {"POST"}),
        _Route("/internal/v1/data-cleanups/ai-sessions", {"POST"}),
    ]
    assert all(route_is_production_allowed(route) for route in routes)


def test_production_surface_rejects_legacy_public_and_websocket_routes():
    routes = [
        _Route("/api/v1/ai/chat", {"POST"}),
        _Route("/api/v1/ops/tasks/{tid}/approve", {"POST"}),
        _Route("/api/v1/ops/k8s/execute", {"POST"}),
        _Route("/api/v1/shell/ws", None),
    ]
    assert all(not route_is_production_allowed(route) for route in routes)


def test_filter_does_not_mutate_input_and_returns_retired_inventory():
    routes = [_Route("/health", {"GET"}), _Route("/api/v1/ai/flows", {"GET"})]
    kept, retired = filter_production_routes(routes)
    assert kept == [routes[0]]
    assert retired == [routes[1]]
    assert routes == [routes[0], routes[1]]


def test_data_cleanup_import_does_not_create_legacy_sqlite(tmp_path):
    """Production route registration must not acquire the migration database."""

    env = os.environ.copy()
    env["AIOPS_DATA_DIR"] = str(tmp_path)
    env["PYTHONPATH"] = str(Path(__file__).parents[1] / "ai-orchestrator")
    result = subprocess.run(
        [sys.executable, "-c", "import data_cleanup_api"],
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    assert not (tmp_path / "ai-sessions.db").exists()


def test_filter_prunes_lazy_included_router_without_dropping_allowed_child():
    """FastAPI's lazy router wrapper must retain the internal cleanup route."""

    router = APIRouter()

    @router.post("/internal/v1/data-cleanups/ai-sessions")
    def cleanup():
        return {"ok": True}

    @router.get("/api/v1/legacy")
    def legacy():
        return {"legacy": True}

    app = FastAPI()
    app.include_router(router)
    kept, _ = filter_production_routes(app.router.routes)
    app.router.routes[:] = kept

    with TestClient(app) as client:
        assert client.post("/internal/v1/data-cleanups/ai-sessions").status_code == 200
        assert client.get("/api/v1/legacy").status_code == 404
