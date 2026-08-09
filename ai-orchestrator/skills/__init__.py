"""Skill 模块包 — 标准化智能运维技能。

包含:
- init_skills(): 注册 7 个标准 Skill
- init_experts(): 注册专家（组合多个 Skill）
"""
from .observability import register_observability_skill
from .infra import register_infra_skill
from .rca_skill import register_rca_skill
from .rag_skill import register_rag_skill
from .vm_ops import register_vm_skill
from .alert_ops import register_alert_skill
from .automation import register_automation_skill
from .diagnose import register_diagnose_skill
from .experts import register_experts


def init_skills():
    """注册全部标准 Skill"""
    register_observability_skill()
    register_infra_skill()
    register_rca_skill()
    register_rag_skill()
    register_vm_skill()
    register_alert_skill()
    register_automation_skill()
    register_diagnose_skill()


def init_experts():
    """注册专家（组合多个 Skill）"""
    register_experts()
