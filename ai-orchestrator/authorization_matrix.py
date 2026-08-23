"""V9.3 Phase 13 — P13.1 Machine-readable Authorization Matrix（服务端安全测试共用 fixture）。

维度：principal/tenant/cluster/namespace/resource/capability/action/risk/confirmation/approval。
数据源注册不成为新的授权维度（P13.4：注册映射不能绕过既有 tenant/cluster/resource authorization）。
""" 
from __future__ import annotations

from dataclasses import dataclass


class AuthzError(ValueError):
    def __init__(self, code: str, message: str):
        self.error_code = code
        super().__init__(message)


# 角色 → capability 集（服务端权威，前端隐藏不算安全控制）
# P19.6: ai.chat = 对话型 capability（只读，不创建 Investigation Run）。所有角色均可对话，
# 因为对话内部 Agent 调用查询工具仍需各自的 observability.*.read capability（Tool Registry 层约束）。
# ai.chat 绝不等于 ai.investigate：对话不触发 ManualBoundary 建 Run。
ROLE_CAPABILITIES: dict[str, set] = {
    "viewer": {"observability.read", "knowledge.read", "ai.chat"},
    "engineer": {"observability.read", "observability.logs.read", "ai.investigate",
                 "ai.chat", "knowledge.read"},
    "operator": {"observability.read", "observability.logs.read", "ai.investigate",
                 "ai.chat", "change.read", "capacity.read", "action.confirm"},
    "admin": {"observability.read", "observability.logs.read", "ai.investigate",
              "ai.chat", "change.read", "capacity.read", "action.confirm", "action.approve",
              "knowledge.read", "knowledge.write", "system.admin"},
}


@dataclass
class AuthzRule:
    """一条授权规则：principal 在 tenant/cluster/namespace/resource 上对 capability/action 的权限。"""
    principal: str
    tenant_id: str
    cluster_id: str
    namespace: str = "*"
    resource: str = "*"
    capability: str = ""
    action: str = "*"
    risk_max: str = "R0"
    require_confirmation: bool = False
    require_approval: bool = False


class AuthorizationMatrix:
    """P13.1 — machine-readable 授权矩阵；服务端独立拒绝所有越权（Gate 13）。

    service_account_roles：已验证用户 principal → 权威角色 的显式映射（开发期/服务账号
    用；生产应从 query-api/MySQL 权威角色 SoT 加载）。未映射的 principal 走前缀映射，
    默认 fail-closed（viewer，无 run 创建等敏感 capability）。
    """

    def __init__(self, service_account_roles: dict[str, str] | None = None) -> None:
        self._rules: list[AuthzRule] = []
        self._service_account_roles: dict[str, str] = service_account_roles or {}

    def add_rule(self, rule: AuthzRule) -> None:
        self._rules.append(rule)

    def authorize(
        self, *, principal: str, tenant_id: str, cluster_id: str, capability: str,
        namespace: str = "*", resource: str = "*", action: str = "*",
        risk: str = "R0",
    ) -> None:
        """服务端授权检查：任一维度不匹配即拒绝（fail-closed）。"""
        # 角色 capability 校验（服务端权威，非前端隐藏）
        role = self._role_of(principal)
        role_caps = ROLE_CAPABILITIES.get(role, set())
        if capability not in role_caps:
            raise AuthzError("CAPABILITY_DENIED", f"principal {principal} 无 capability {capability}")
        # 规则匹配：tenant/cluster 必配（支持 "*" 通配，与 namespace/resource 一致），
        # namespace/resource/capability 最小化
        matched = [
            r for r in self._rules
            if r.principal == principal
            and (r.tenant_id == "*" or r.tenant_id == tenant_id)
            and (r.cluster_id == "*" or r.cluster_id == cluster_id)
            and (r.namespace == "*" or r.namespace == namespace)
            and (r.resource == "*" or r.resource == resource)
            and (r.capability == "" or r.capability == capability)
            and (r.action == "*" or r.action == action)
        ]
        if not matched:
            raise AuthzError("AUTHZ_DENIED", f"principal {principal} 在 {tenant_id}/{cluster_id} 无此权限")
        # risk 上限
        if risk > "R" + matched[0].risk_max[1:] and matched[0].risk_max != "R4":
            # 简化 risk 比较：规则 risk_max 以上拒绝（R4 视为最严，允许所有）
            raise AuthzError("RISK_EXCEEDED", f"action risk {risk} 超规则上限 {matched[0].risk_max}")
        # confirmation/approval 要求
        if matched[0].require_confirmation:
            if not self._confirmed(principal, capability):
                raise AuthzError("CONFIRMATION_REQUIRED", "需 requester 显式确认")
        if matched[0].require_approval:
            if not self._approved(principal):
                raise AuthzError("APPROVAL_REQUIRED", "需独立 approver 批准")

    def _role_of(self, principal: str) -> str:
        # 显式服务账号角色映射优先（P13 接线：已验证 user principal 权威角色；
        # 生产从 query-api/MySQL 权威角色 SoT 加载，此映射为开发期/服务账号用）
        if principal in self._service_account_roles:
            return self._service_account_roles[principal]
        # 前缀映射（简化；真实应从用户目录 SoT）
        if principal.startswith("admin"):
            return "admin"
        if principal.startswith("op_"):
            return "operator"
        if principal.startswith("eng_"):
            return "engineer"
        return "viewer"

    def _confirmed(self, principal: str, capability: str) -> bool:
        return getattr(self, "_confirmations", {}).get(principal) is True

    def _approved(self, principal: str) -> bool:
        return getattr(self, "_approvals", {}).get(principal) is True

    def set_confirmed(self, principal: str) -> None:
        if not hasattr(self, "_confirmations"):
            self._confirmations = {}
        self._confirmations[principal] = True

    def set_approved(self, principal: str) -> None:
        if not hasattr(self, "_approvals"):
            self._approvals = {}
        self._approvals[principal] = True

    # ── P13.2 Public API Tamper 助手 ────────────────────────────────────
    def authorize_request(
        self, *, principal: str, role: str, tenant_id: str, cluster_id: str,
        capability: str, resource: str = "*",
    ) -> None:
        """服务端按角色+tenant+cluster 授权；role 篡改由服务端 ROLE_CAPABILITIES 拒绝（非前端）。"""
        # 服务端用 principal 的权威角色（忽略前端传的 role 参数，防 localStorage tamper）
        self.authorize(principal=principal, tenant_id=tenant_id, cluster_id=cluster_id,
                       capability=capability, resource=resource)
