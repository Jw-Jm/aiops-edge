import pytest
from llm_mock import is_mock_enabled, mock_llm_response


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
