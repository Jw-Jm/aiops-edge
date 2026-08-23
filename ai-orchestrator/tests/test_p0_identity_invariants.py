"""P0-1 身份不变量测试（审计阻断项 B0-01）。

冻结契约要求：
- system principal 的 session_id 必须为空（不持有认证会话）。
- user principal 必须绑定真实认证会话（不得自动生成 session）。
- 禁止用 `session_id or uuid4()` 为 system 伪造非空 session。
"""
import pytest


def _issuer():
    from trusted_context_issuer import TrustedContextIssuer

    return TrustedContextIssuer(
        private_key="test-private-key",
        issuer="ai-orchestrator",
        audience="ai-apm-query-go",
        lifetime_seconds=30,
    )


def test_system_principal_has_empty_session():
    from trusted_context_issuer import TrustedContextIssuer

    issuer = _issuer()
    claims = issuer.build_claims(
        tenant_id="t1", cluster_id="c1", capability="observability.logs.read",
        run_id="run-1", principal_type="system", principal_id="sys-1",
        session_id=None,
    )
    assert claims["session_id"] == ""
    assert claims["principal_type"] == "system"


def test_system_principal_rejects_nonempty_session():
    from trusted_context_issuer import TrustedContextError

    issuer = _issuer()
    with pytest.raises(TrustedContextError):
        issuer.build_claims(
            tenant_id="t1", cluster_id="c1", capability="observability.logs.read",
            run_id="run-1", principal_type="system", principal_id="sys-1",
            session_id="fake-session-for-system",
        )


def test_user_principal_requires_real_session():
    from trusted_context_issuer import TrustedContextError

    issuer = _issuer()
    # user principal 不传 session_id → 拒绝（不得自动生成）
    with pytest.raises(TrustedContextError):
        issuer.build_claims(
            tenant_id="t1", cluster_id="c1", capability="observability.logs.read",
            run_id="run-1", principal_type="user", principal_id="u-1",
            session_id=None,
        )


def test_user_principal_accepts_real_session():
    issuer = _issuer()
    claims = issuer.build_claims(
        tenant_id="t1", cluster_id="c1", capability="observability.logs.read",
        run_id="run-1", principal_type="user", principal_id="u-1",
        session_id="44444444-4444-4444-8444-444444444444",
    )
    assert claims["session_id"] == "44444444-4444-4444-8444-444444444444"


def test_no_auto_generated_session():
    """断言：无论何种 principal，session 都不应被自动 uuid4 生成（除非显式 user session）。"""
    issuer = _issuer()
    claims = issuer.build_claims(
        tenant_id="t1", cluster_id="c1", capability="observability.logs.read",
        run_id="run-1", principal_type="system", principal_id="sys-1",
        session_id=None,
    )
    # system 空 session，绝不自动生成
    assert claims["session_id"] == ""
