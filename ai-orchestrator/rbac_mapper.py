"""PE.4 RBAC 最小权限 verb 映射 — V9.3 Execution Production Enablement。

将 contract.allowed_actions 映射为最小 K8s RBAC verb（PE.4）：
- 白名单映射（action → verb），白名单外 action 拒绝（缺省拒绝）。
- 生成 verb 永不包含 delete/create/evacuate（最小权限）。
- verify_min_privilege：校验无危险 verb。
"""
from __future__ import annotations

from typing import Set

# action → 最小 K8s verb（白名单；缺省拒绝）
ACTION_VERB_MAP = {
    "restart": {"get", "list", "patch:restart"},
    "scale": {"get", "list", "patch:scale"},
}

# 危险 verb（最小权限下永不生成/绝不通过）
_DANGEROUS_VERBS = {"delete", "create", "evacuate"}


class ForbiddenAction(ValueError):
    def __init__(self, action: str):
        self.error_code = "FORBIDDEN_ACTION"
        super().__init__(f"action 白名单外，拒绝生成 verb: {action}")


class K8sVerbMapper:
    """contract.allowed_actions → 最小 K8s RBAC verb（内存 MVP）。"""

    def map_verbs(self, allowed_actions) -> Set[str]:
        """把 allowed_actions 映射为最小 verb；白名单外 action → ForbiddenAction。"""
        verbs: Set[str] = set()
        for action in allowed_actions:
            mapped = ACTION_VERB_MAP.get(action)
            if mapped is None:
                raise ForbiddenAction(action)
            verbs |= mapped
        return verbs

    def verify_min_privilege(self, verbs: Set[str]) -> bool:
        """校验 verb 集不含危险 verb（delete/create/evacuate）。"""
        return not (verbs & _DANGEROUS_VERBS)
