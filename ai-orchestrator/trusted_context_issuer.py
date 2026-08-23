"""P7.2 TrustedContextIssuer — V9.3 Phase7 按 tenant/cluster/capability 签发 TrustedRequestContext V2。

安全边界：
- capability 只能来自 Tool Registry（KNOWN_CAPABILITIES），禁止 LLM/Agent 生成或篡改 capability 字符串。
- 每次调用生成唯一 request_id / session_id / nonce（防 replay）。
- 短时效（默认 30s）：issued_at / expires_at 窗口。
- 复用现有 trust root（不 rotate、不新建签发体系）。
- 签发 scope 严格来自当前 Run 的 tenant/cluster/capability，不自动扩大。

`issue()` 返回可验证的 EdDSA JWS；`build_claims()` 返回同一组结构化 claims
（供 InternalQueryClient 做调用追踪 / 测试断言），每次调用唯一字段保持一致。
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
from typing import Any, Dict
from uuid import uuid4

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from tool_registry import KNOWN_CAPABILITIES
from trusted_context import TrustedContextError, sign_trusted_request_context_v2

DEFAULT_ISSUER = "ai-orchestrator"
DEFAULT_AUDIENCE = "ai-apm-query-go"
DEFAULT_LIFETIME_SECONDS = 30


class TrustedContextIssuer:
    """为 InternalQueryClient 每次调用签发唯一 TrustedRequestContext V2。"""

    def __init__(
        self,
        *,
        private_key: Ed25519PrivateKey,
        issuer: str = DEFAULT_ISSUER,
        audience: str = DEFAULT_AUDIENCE,
        lifetime_seconds: int = DEFAULT_LIFETIME_SECONDS,
    ) -> None:
        self._private_key = private_key
        self._issuer = issuer
        self._audience = audience
        self._lifetime = timedelta(seconds=lifetime_seconds)

    def build_claims(
        self,
        *,
        tenant_id: str,
        cluster_id: str,
        capability: str,
        run_id: str,
        principal_type: str = "user",
        principal_id: str,
        session_id: str | None = None,
        source: str = "planner",
    ) -> Dict[str, Any]:
        """构造一组唯一、可签名的 TrustedRequestContext V2 claims（不签名）。

        capability 必须是 KNOWN_CAPABILITIES 之一（来自 Tool Registry），否则拒绝。
        """
        if capability not in KNOWN_CAPABILITIES:
            raise TrustedContextError("invalid_context")
        # 身份不变量（审计 P0-1）：按 principal_type 强制校验 session。
        # - system principal：session_id 必须为空（不持有认证会话）。
        # - user principal：session_id 必须为非空真实认证会话（不得自动生成）。
        # 禁止用 `session_id or uuid4()` 自动生成会话（会为 system 伪造非空 session，
        # 且 user 未绑定真实认证会话）。身份必须从已验证的 RunInvocationContext 传播。
        if principal_type == "system":
            if session_id not in (None, ""):
                raise TrustedContextError("invalid_identity: system principal must have empty session_id")
            effective_session = ""
        else:
            if not session_id:
                raise TrustedContextError("invalid_identity: user principal requires real authenticated session_id")
            effective_session = session_id
        now = datetime.now(timezone.utc)
        return {
            "version": 1,
            "context_type": "trusted_request",
            "issuer": self._issuer,
            "audience": self._audience,
            "request_id": uuid4(),
            "run_id": run_id,
            "principal_type": principal_type,
            "principal_id": principal_id,
            "session_id": effective_session,
            "tenant_id": tenant_id,
            "scope_kind": "cluster",
            "cluster_id": cluster_id,
            "capability": capability,
            "source": source,
            "issued_at": now,
            "expires_at": now + self._lifetime,
            "nonce": uuid4(),
        }

    def issue(self, **kwargs) -> str:
        """签发 TrustedRequestContext V2 的 EdDSA JWS（参数同 build_claims）。"""
        claims = self.build_claims(**kwargs)
        return sign_trusted_request_context_v2(claims, self._private_key)
