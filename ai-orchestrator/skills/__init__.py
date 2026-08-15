"""Skill 模块包 — 标准化智能运维技能。

包含:
- init_skills(): 注册全部标准 Skill（D1 占位：真实加载逻辑由 D3 经 skill_loader 接管）
- init_experts(): 注册专家（组合多个 Skill）
"""
from .experts import register_experts


def init_skills():
    """占位空壳：技能已迁移为 SKILL.md 文件，加载逻辑由 D3 的 skill_loader 接管。"""
    return


def init_experts():
    """注册专家（组合多个 Skill）"""
    register_experts()
