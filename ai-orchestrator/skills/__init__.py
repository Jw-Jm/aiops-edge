"""Skill 模块包 — 标准化智能运维技能。

包含:
- init_skills(): 注册工具函数并从 SKILL.md 文件加载全部标准 Skill（文件化加载）
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

_BUILTIN_REGISTERS = (
    register_observability_skill,
    register_infra_skill,
    register_rca_skill,
    register_rag_skill,
    register_vm_skill,
    register_alert_skill,
    register_automation_skill,
    register_diagnose_skill,
)


def init_skills():
    """注册工具函数并从 SKILL.md 文件加载全部标准 Skill（幂等）。"""
    for reg in _BUILTIN_REGISTERS:
        reg()
    from skill_loader import load_skills
    from skill_registry import SkillRegistry
    SkillRegistry.load_from_skills(load_skills())


def init_experts():
    """注册专家（组合多个 Skill）"""
    register_experts()
