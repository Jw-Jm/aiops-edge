"""Compatibility ASGI entrypoint for the stateless Investigation Worker.

The implementation lives in ``apps.investigation`` so the worker has an
independent composition root and never imports the gateway ``main`` module.
"""
from __future__ import annotations

import os

os.environ["INVESTIGATION_WORKER_MODE"] = "true"

from apps.investigation import app

_ALLOWED_PREFIXES = ("/internal/v1/run-invocations", "/health", "/readyz", "/metrics")
