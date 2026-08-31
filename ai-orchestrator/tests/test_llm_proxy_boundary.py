"""Production LLM egress guard regression tests."""

def test_k8sgpt_production_uses_proxy_token_and_signed_metadata(monkeypatch):
    import tools

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.setenv("LLM_PROXY_URL", "http://proxy:8080")
    monkeypatch.setenv("LLM_PROXY_TOKEN", "proxy-token")
    monkeypatch.setenv("QUERY_API_URL", "http://query-api:8080/api/v1")
    monkeypatch.setenv("AIOPS_SYSTEM_TENANT_ID", "7ed01afc-cc79-4ecd-8767-a2befa6168ad")
    monkeypatch.setenv("AIOPS_SYSTEM_CLUSTER_ID", "91771a6e-9c2d-11f1-8271-bea176fe9f9f")
    monkeypatch.setenv("TRUSTED_CONTEXT_PRIVATE_KEY", "invalid-in-test")
    monkeypatch.setattr(
        tools,
        "signed_query_api_request",
        lambda *args, **kwargs: b'{"data":{"provider":"deepseek","model":"deepseek-chat","base_url":"https://evil.invalid"}}',
    )
    tools._LLM_CONFIG_CACHE.update(config=None, fetched_at=0.0)

    cfg = tools._fetch_llm_config_for_k8sgpt()

    assert cfg == {
        "api_key": "proxy-token",
        "model": "deepseek-chat",
        "base_url": "http://proxy:8080/v1/proxy/deepseek",
        "proxy_only": True,
    }


def test_k8sgpt_production_does_not_fallback_to_provider_key(monkeypatch):
    import tools

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.delenv("LLM_PROXY_URL", raising=False)
    monkeypatch.delenv("LLM_PROXY_TOKEN", raising=False)
    monkeypatch.setattr(
        tools.urllib.request,
        "urlopen",
        lambda *args, **kwargs: (_ for _ in ()).throw(AssertionError("direct provider fetch")),
    )
    tools._LLM_CONFIG_CACHE.update(config=None, fetched_at=0.0)

    assert tools._fetch_llm_config_for_k8sgpt() is None


def test_orchestrator_rejects_direct_provider_config_in_production(monkeypatch):
    import orchestrator

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.setenv("LLM_PROXY_URL", "http://proxy:8080")
    monkeypatch.setenv("LLM_PROXY_TOKEN", "proxy-token")
    orchestrator._LLM_KEY_HOLDER["api_key"] = "stale"

    # Exercise the real method on the existing brain object; an external caller
    # attempting to inject a provider URL/key must clear the capability.
    orchestrator.brain.set_llm_config(
        {"api_key": "provider-secret", "base_url": "https://api.openai.com/v1", "model": "gpt-4o"}
    )
    assert orchestrator._LLM_KEY_HOLDER["api_key"] == ""
    assert orchestrator.brain.llm_config is None
