"""Production tools must not fall back to the retired MySQL graph snapshot."""

from __future__ import annotations

from invocation_scope import InvocationScope


def _scope() -> InvocationScope:
    return InvocationScope(
        principal_type="system",
        principal_id="11111111-1111-4111-8111-111111111111",
        session_id=None,
        tenant_id="22222222-2222-4222-8222-222222222222",
        cluster_id="33333333-3333-4333-8333-333333333333",
        request_id="44444444-4444-4444-8444-444444444444",
        source="test",
    )


def test_production_without_graph_backend_uses_query_api(monkeypatch):
    import tools

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.delenv("AIOPS_DEPLOYMENT_MODE", raising=False)
    monkeypatch.delenv("GRAPH_BACKEND", raising=False)
    loaded = {"called": False}

    def fail_legacy_load():
        loaded["called"] = True
        raise AssertionError("legacy graph snapshot must not be loaded")

    monkeypatch.setattr(tools, "_get_json", lambda *args, **kwargs: {"services": []})
    monkeypatch.setitem(tools.__dict__, "_legacy_graph_snapshot_enabled", lambda: False)
    monkeypatch.setattr(tools, "_context_for_cluster", lambda *_args: _scope())
    monkeypatch.setattr(tools, "_cluster_param", lambda *_args: "cluster_id=" + _scope().cluster_id)
    result = tools.get_service_list(cluster_id=_scope().cluster_id, request_context=_scope())
    assert result == "[]"
    assert loaded["called"] is False


def test_legacy_graph_snapshot_requires_explicit_nonproduction_opt_in(monkeypatch):
    import tools

    monkeypatch.delenv("AIOPS_ENV", raising=False)
    monkeypatch.delenv("AIOPS_DEPLOYMENT_MODE", raising=False)
    monkeypatch.delenv("GRAPH_BACKEND", raising=False)
    assert tools._legacy_graph_snapshot_enabled() is False
    monkeypatch.setenv("GRAPH_BACKEND", "legacy_mysql")
    assert tools._legacy_graph_snapshot_enabled() is True
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    assert tools._legacy_graph_snapshot_enabled() is False
