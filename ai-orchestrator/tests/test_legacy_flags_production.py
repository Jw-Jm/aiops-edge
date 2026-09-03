"""Retired in-process mutation flags cannot be re-enabled (P1-A1 后).

P1-A1: legacy in-process direct mutation 已物理退役——_direct_mutation_enabled()
恒 False（不读任何 env）。execution-after-approval / approval-compat 仍由
对应 flag 门控（production fail-closed；non-production 显式测试可用）。
"""

from __future__ import annotations


def test_direct_mutation_is_permanently_disabled_in_all_environments(monkeypatch):
    """legacy direct mutation 在任意环境（含 dev/flag=1）都恒为 False。"""
    import main

    for env_mode in ("production", "development"):
        monkeypatch.setenv("AIOPS_ENV", env_mode)
        monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", env_mode)
        assert main._direct_mutation_enabled() is False


def test_execution_after_approval_remains_production_fail_closed(monkeypatch):
    import main

    monkeypatch.setenv("AIOPS_ENV", "production")
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "production")
    monkeypatch.setenv("EXECUTION_AFTER_APPROVAL", "1")
    assert main._execution_after_approval_enabled() is False
    assert main._legacy_approval_compat_enabled() is False


def test_nonproduction_execution_after_approval_remains_explicitly_testable(monkeypatch):
    import main

    monkeypatch.setenv("AIOPS_ENV", "development")
    monkeypatch.setenv("AIOPS_DEPLOYMENT_MODE", "development")
    monkeypatch.setenv("EXECUTION_AFTER_APPROVAL", "1")
    assert main._execution_after_approval_enabled() is True
    assert main._legacy_approval_compat_enabled() is True
