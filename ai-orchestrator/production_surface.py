"""Production Gateway HTTP surface.

The historical Orchestrator module still contains development and migration
handlers.  They may remain importable for local compatibility, but production
must expose only the signed internal boundary and health/observability probes.
Keeping this policy in a small side-effect-free module makes the boundary
testable without importing the heavyweight Gateway composition root.
"""
from __future__ import annotations

from collections.abc import Iterable
from types import SimpleNamespace
from typing import Any


# Exact path/method pairs are intentional.  A path that is not listed here is
# not a production Gateway capability, even if a legacy handler still exists
# in ``main.py`` for development or migration use.
PRODUCTION_ROUTE_ALLOWLIST = frozenset(
    {
        ("/health", "GET"),
        ("/api/v1/health", "GET"),
        ("/readyz", "GET"),
        ("/metrics", "GET"),
        ("/internal/v1/run-invocations", "POST"),
        ("/internal/v1/chat", "POST"),
        ("/internal/v1/run-controls/{operation}", "POST"),
        ("/internal/v1/data-cleanups/ai-sessions", "POST"),
    }
)


def route_is_production_allowed(route: Any) -> bool:
    """Return whether a Starlette/FastAPI route belongs on the prod surface.

    WebSocket routes and mounts have no exact HTTP method identity and are
    rejected by default.  A route with multiple methods is allowed only when
    every method is explicitly listed for that exact path.
    """

    path = str(getattr(route, "path", "") or "")
    methods = frozenset(str(method).upper() for method in (getattr(route, "methods", None) or ()))
    if not path or not methods:
        return False
    return _path_methods_are_allowed(path, methods)


def _path_methods_are_allowed(path: str, methods: Iterable[str]) -> bool:
    return bool(path) and bool(methods) and all(
        (path, method) in PRODUCTION_ROUTE_ALLOWLIST for method in methods
    )


def _join_route_prefix(prefix: str, path: str) -> str:
    prefix = str(prefix or "").rstrip("/")
    path = str(path or "")
    if not path.startswith("/"):
        path = "/" + path
    return (prefix + path) or "/"


def _filter_route_tree(
    routes: Iterable[Any], prefix: str = ""
) -> tuple[list[Any], list[Any]]:
    """Filter FastAPI 0.141's lazy ``_IncludedRouter`` tree in place.

    Newer FastAPI versions keep included routers as lazy wrapper nodes rather
    than flattening them into ``app.router.routes``.  Dropping those wrappers
    wholesale would also drop the explicitly allowed cleanup endpoint.  Walk
    their original routers, apply the include prefix to child paths, and keep
    a wrapper only when at least one child is allowed.
    """

    kept: list[Any] = []
    retired: list[Any] = []
    for route in routes:
        original_router = getattr(route, "original_router", None)
        if original_router is not None and hasattr(original_router, "routes"):
            context = getattr(route, "include_context", None)
            child_prefix = _join_route_prefix(prefix, getattr(context, "prefix", ""))
            child_kept, child_retired = _filter_route_tree(
                original_router.routes, child_prefix
            )
            original_router.routes[:] = child_kept
            retired.extend(child_retired)
            if child_kept:
                kept.append(route)
            else:
                retired.append(route)
            continue

        path = _join_route_prefix(prefix, getattr(route, "path", ""))
        methods = frozenset(
            str(method).upper() for method in (getattr(route, "methods", None) or ())
        )
        identity = SimpleNamespace(path=path, methods=methods)
        (kept if route_is_production_allowed(identity) else retired).append(route)
    return kept, retired


def filter_production_routes(routes: Iterable[Any]) -> tuple[list[Any], list[Any]]:
    """Filter direct and lazily included routes for the production surface."""

    # ``list`` protects callers that pass a generator; included-router child
    # lists are intentionally updated by the production composition root.
    return _filter_route_tree(list(routes))
