"""PE.4 RBAC 最小权限 verb 映射 — TDD 测试（V9.3 Production Enablement）。

覆盖 PE.4：
- T1 allowed_actions → 最小 K8s verb（restart→get/list/patch-restart）
- T2 白名单外 action → 拒绝（不生成 verb）
- T3 delete/create/evacuate 永不在生成 verb 中（最小权限）
- T4 verify_min_privilege（禁危险 verb）
"""
from __future__ import annotations

import pytest

from rbac_mapper import ForbiddenAction, K8sVerbMapper


@pytest.fixture
def mapper():
    return K8sVerbMapper()


# ═══════════════════════════════════════════════════════
#  T1 action → 最小 verb
# ═══════════════════════════════════════════════════════

class TestT1VerbMapping:
    def test_restart_verbs(self, mapper):
        verbs = mapper.map_verbs(["restart"])
        assert "get" in verbs
        assert "list" in verbs
        assert "patch:restart" in verbs

    def test_scale_verbs(self, mapper):
        verbs = mapper.map_verbs(["scale"])
        assert "patch:scale" in verbs


# ═══════════════════════════════════════════════════════
#  T2 白名单外 action 拒绝
# ═══════════════════════════════════════════════════════

class TestT2ForbiddenAction:
    def test_delete_rejected(self, mapper):
        with pytest.raises(ForbiddenAction):
            mapper.map_verbs(["delete"])

    def test_unknown_action_rejected(self, mapper):
        with pytest.raises(ForbiddenAction):
            mapper.map_verbs(["evacuate"])


# ═══════════════════════════════════════════════════════
#  T3 最小权限（禁危险 verb）
# ═══════════════════════════════════════════════════════

class TestT3LeastPrivilege:
    def test_no_dangerous_verbs(self, mapper):
        verbs = mapper.map_verbs(["restart", "scale"])
        assert "delete" not in verbs
        assert "create" not in verbs
        assert "evacuate" not in verbs


# ═══════════════════════════════════════════════════════
#  T4 verify_min_privilege
# ═══════════════════════════════════════════════════════

class TestT4MinPrivilegeVerify:
    def test_min_privilege_passes(self, mapper):
        verbs = mapper.map_verbs(["restart"])
        assert mapper.verify_min_privilege(verbs) is True

    def test_dangerous_verb_fails(self, mapper):
        assert mapper.verify_min_privilege({"delete"}) is False
