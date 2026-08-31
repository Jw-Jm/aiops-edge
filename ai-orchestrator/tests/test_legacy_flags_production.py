"""Retired in-process mutation/approval flags cannot be enabled in production."""

from __future__ import annotations


def test_production_ignores_legacy_mutation_flags(monkeypatch):
    import main

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.setenv("LEGACY_DIRECT_MUTATIONS_ENABLED", "1")
    monkeypatch.setenv("EXECUTION_AFTER_APPROVAL", "1")
    monkeypatch.setenv("LEGACY_APPROVAL_COMPAT", "1")

    assert main._direct_mutation_enabled() is False
    assert main._execution_after_approval_enabled() is False
    assert main._legacy_approval_compat_enabled() is False


def test_nonproduction_migration_flags_remain_explicitly_testable(monkeypatch):
    import main

    monkeypatch.setenv("AIOPS_ENV", "development")
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "development")
    monkeypatch.setenv("LEGACY_DIRECT_MUTATIONS_ENABLED", "1")
    monkeypatch.setenv("EXECUTION_AFTER_APPROVAL", "1")

    assert main._direct_mutation_enabled() is True
    assert main._execution_after_approval_enabled() is True
    assert main._legacy_approval_compat_enabled() is True
