"""Rate-limit bypasses must use the same exact probe boundary as auth."""

from __future__ import annotations


def test_rate_limit_public_probe_paths_are_exact():
    import main

    allowed = {
        "/health",
        "/api/v1/health",
        "/metrics",
        "/docs",
        "/openapi.json",
    }
    for path in allowed:
        assert main._rate_limit_bypass_path_allowed(path) is True
    for path in (
        "/healthz",
        "/health/extra",
        "/api/v1/health/debug",
        "/metrics/debug",
        "/docs/private",
        "/openapi.json.bak",
    ):
        assert main._rate_limit_bypass_path_allowed(path) is False
