"""P0: Run 持久化启动强制化 — factory & readyz"""
import base64
import os
import socket
import threading

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey


def _gen_key_b64() -> str:
    key = Ed25519PrivateKey.generate()
    raw = key.private_bytes_raw()
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


# -- helpers for ephemeral listener --
def _ephemeral_listener():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.listen(1)
    return s, port


# 1. production + all 3 deleted → PersistenceConfigError with 3 missing items
def test_production_missing_all_raises(monkeypatch):
    import run_store_factory as f
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
    for k in ("QUERY_API_URL", "TRUSTED_CONTEXT_PRIVATE_KEY", "INTERNAL_TOKEN"):
        monkeypatch.delenv(k, raising=False)
    from run_persistence import RunStateStore
    fallback = RunStateStore()
    with pytest.raises(f.PersistenceConfigError) as exc:
        f.build_run_state_store(fallback)
    assert set(exc.value.missing_items) == {"QUERY_API_URL", "TRUSTED_CONTEXT_PRIVATE_KEY", "INTERNAL_TOKEN"}


# 2. production + only QUERY_API_URL set → error listing the other two
def test_production_only_query_url_missing_two(monkeypatch):
    import run_store_factory as f
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
    monkeypatch.setenv("QUERY_API_URL", "http://example.com")
    monkeypatch.delenv("TRUSTED_CONTEXT_PRIVATE_KEY", raising=False)
    monkeypatch.delenv("INTERNAL_TOKEN", raising=False)
    from run_persistence import RunStateStore
    fallback = RunStateStore()
    with pytest.raises(f.PersistenceConfigError) as exc:
        f.build_run_state_store(fallback)
    assert "TRUSTED_CONTEXT_PRIVATE_KEY" in exc.value.missing_items
    assert "INTERNAL_TOKEN" in exc.value.missing_items
    assert "QUERY_API_URL" not in exc.value.missing_items


# 3. development + none set → returns (fallback, "memory"), no raise
def test_development_missing_returns_memory(monkeypatch):
    import run_store_factory as f
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "development")
    monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
    for k in ("QUERY_API_URL", "TRUSTED_CONTEXT_PRIVATE_KEY", "INTERNAL_TOKEN"):
        monkeypatch.delenv(k, raising=False)
    from run_persistence import RunStateStore
    fallback = RunStateStore()
    store, backend = f.build_run_state_store(fallback)
    assert store is fallback
    assert backend == "memory"


# 4. production + full config + unreachable → ConnectionError/OSError
def test_production_full_unreachable_raises(monkeypatch):
    import run_store_factory as f
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
    monkeypatch.setenv("QUERY_API_URL", "http://127.0.0.1:1")
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", _gen_key_b64())
    monkeypatch.setenv("INTERNAL_TOKEN", "tok")
    from run_persistence import RunStateStore
    fallback = RunStateStore()
    with pytest.raises((ConnectionError, OSError)):
        f.build_run_state_store(fallback)


# 5. production + full config + reachable → ("remote"), persisted True
def test_production_full_reachable_returns_remote(monkeypatch):
    import run_store_factory as f
    srv, port = _ephemeral_listener()
    try:
        monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
        monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
        monkeypatch.setenv("QUERY_API_URL", f"http://127.0.0.1:{port}")
        monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", _gen_key_b64())
        monkeypatch.setenv("INTERNAL_TOKEN", "tok")
        from run_persistence import RunStateStore
        fallback = RunStateStore()
        store, backend = f.build_run_state_store(fallback)
        assert backend == "remote"
        assert store.persisted is True
    finally:
        srv.close()


# 6. resolve_deployment_mode defaults
def test_resolve_deployment_mode_defaults(monkeypatch):
    import run_store_factory as f
    monkeypatch.delenv("AIOPS_DEPLOYMENT_MODE", raising=False)
    monkeypatch.delenv("RUN_PERSISTENCE_REQUIRED", raising=False)
    assert f.resolve_deployment_mode() == "development"
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    assert f.resolve_deployment_mode() == "production"
    monkeypatch.delenv("AIOPS_DEPLOYMENT_MODE", raising=False)
    monkeypatch.setenv("RUN_PERSISTENCE_REQUIRED", "1")
    assert f.resolve_deployment_mode() == "production"
    monkeypatch.setenv("RUN_PERSISTENCE_REQUIRED", "true")
    assert f.resolve_deployment_mode() == "production"
    monkeypatch.setenv("RUN_PERSISTENCE_REQUIRED", "yes")
    assert f.resolve_deployment_mode() == "production"


def test_resolve_deployment_mode_run_persistence_required_variants(monkeypatch):
    import run_store_factory as f
    monkeypatch.delenv("AIOPS_DEPLOYMENT_MODE", raising=False)
    for v in ("1", "true", "yes", "True", "YES"):
        monkeypatch.setenv("RUN_PERSISTENCE_REQUIRED", v)
        assert f.resolve_deployment_mode() == "production"


def test_required_persistence_env_reads(monkeypatch):
    import run_store_factory as f
    monkeypatch.setenv("QUERY_API_URL", "http://q")
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", "k")
    monkeypatch.setenv("INTERNAL_TOKEN", "t")
    env = f.required_persistence_env()
    assert env["QUERY_API_URL"] == "http://q"
    assert env["TRUSTED_CONTEXT_PRIVATE_KEY"] == "k"
    assert env["INTERNAL_TOKEN"] == "t"


def test_check_control_plane_reachable_unreachable():
    import run_store_factory as f
    with pytest.raises((ConnectionError, OSError)):
        f.check_control_plane_reachable("http://127.0.0.1:1", timeout=1.0)


def test_check_control_plane_reachable_reachable():
    import run_store_factory as f
    srv, port = _ephemeral_listener()
    try:
        # should not raise
        f.check_control_plane_reachable(f"http://127.0.0.1:{port}", timeout=1.0)
    finally:
        srv.close()


def test_is_ready_helper():
    import run_store_factory as f
    assert f.is_ready("production", "remote") is True
    assert f.is_ready("production", "memory") is False
    assert f.is_ready("development", "memory") is True
    assert f.is_ready("development", "remote") is True
