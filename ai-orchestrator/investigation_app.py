"""Narrow ASGI entrypoint for the stateless Investigation Worker.

The shared ``main`` module still contains the compatibility Chat gateway, but
this entrypoint sets the worker role before import and rejects every public or
legacy route.  The worker therefore owns only signed Run invocations and
health/metrics probes while rebuilding durable state from query-api.
"""
from __future__ import annotations

import os

os.environ["INVESTIGATION_WORKER_MODE"] = "true"

from main import app as _main_app

_ALLOWED_PREFIXES = ("/internal/v1/run-invocations", "/health", "/readyz", "/metrics")


class InvestigationWorkerApp:
    def __init__(self, inner):
        self.inner = inner

    async def __call__(self, scope, receive, send):
        if scope.get("type") != "http":
            await self.inner(scope, receive, send)
            return
        path = str(scope.get("path") or "")
        if any(path.startswith(prefix) for prefix in _ALLOWED_PREFIXES):
            await self.inner(scope, receive, send)
            return
        body = b'{"error":"investigation_worker_route_not_available"}'
        await send({"type": "http.response.start", "status": 404,
                    "headers": [(b"content-type", b"application/json"),
                                (b"content-length", str(len(body)).encode())]})
        await send({"type": "http.response.body", "body": body})


app = InvestigationWorkerApp(_main_app)
