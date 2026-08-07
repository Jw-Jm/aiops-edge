import pytest
from llm_mock import is_mock_enabled, mock_llm_response, should_skip_llm


def test_mock_disabled_by_default(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert is_mock_enabled() is False


def test_mock_enabled_when_true(monkeypatch):
    monkeypatch.setenv("LLM_MOCK", "true")
    assert is_mock_enabled() is True


def test_mock_response_shape():
    resp = mock_llm_response("who is the caller?")
    assert isinstance(resp, str) and len(resp) > 0
    assert "RCA" in resp or "analysis" in resp.lower()


def test_should_skip_llm_skips_when_no_key_and_not_mock(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert should_skip_llm(None) is True
    assert should_skip_llm({}) is True
    assert should_skip_llm({"api_key": ""}) is True
    assert should_skip_llm({"api_key": "sk-xxx"}) is False


def test_should_skip_llm_not_skip_when_mock_even_without_key(monkeypatch):
    monkeypatch.setenv("LLM_MOCK", "true")
    assert should_skip_llm(None) is False
    assert should_skip_llm({}) is False
