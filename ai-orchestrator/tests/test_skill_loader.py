"""SKILL.md 加载器测试: SkillFile + load_skills"""
import os
import shutil

import pytest
import yaml

from skill_registry import ToolRegistry
from skill_loader import SkillFile, load_skills, builtin_skills_dir


@pytest.fixture(autouse=True)
def register_dummy_tool():
    if not ToolRegistry.get("my_tool"):
        ToolRegistry.register(name="my_tool", description="测试工具")(lambda: "ok")
    yield


def _mk_skill(skill_dir: str, skill_name: str, **overrides):
    """在 skill_dir 下写一个合法 SKILL.md，返回 skill_dir 路径。"""
    meta = {
        "name": skill_name,
        "description": "测试技能描述",
        "when_to_use": "需要诊断故障时使用",
        "activation": {"mode": "keyword", "keywords": ["诊断", "故障"]},
        "tools": [{"name": "my_tool", "impl": "builtin", "class": "read"}],
    }
    meta.update(overrides)
    os.makedirs(skill_dir, exist_ok=True)
    text = "---\n" + yaml.safe_dump(meta, allow_unicode=True, sort_keys=False) + "---\n这是技能正文。"
    with open(os.path.join(skill_dir, "SKILL.md"), "w", encoding="utf-8") as f:
        f.write(text)
    return skill_dir


def test_load_valid_dir_all_loaded(tmp_path):
    _mk_skill(str(tmp_path / "s1"), "skill.one")
    _mk_skill(str(tmp_path / "s2"), "skill.two")
    skills = load_skills(str(tmp_path))
    assert set(skills) == {"skill.one", "skill.two"}
    sf = skills["skill.one"]
    assert isinstance(sf, SkillFile)
    assert sf.description == "测试技能描述"
    assert sf.when_to_use == "需要诊断故障时使用"
    assert sf.activation_keywords == ["诊断", "故障"]
    assert sf.tools == ["my_tool"]
    assert "技能正文" in sf.system_prompt
    assert sf.source.endswith("SKILL.md")


def test_missing_required_field_raises(tmp_path):
    with pytest.raises(ValueError, match="when_to_use"):
        _mk_skill(str(tmp_path / "bad"), "skill.bad", when_to_use=None)
        load_skills(str(tmp_path))


def test_missing_name_raises(tmp_path):
    with pytest.raises(ValueError, match="name"):
        _mk_skill(str(tmp_path / "bad"), "skill.bad", name=None)
        load_skills(str(tmp_path))


def test_unregistered_tool_raises(tmp_path):
    with pytest.raises(ValueError, match="unknown_tool"):
        _mk_skill(str(tmp_path / "bad"), "skill.bad",
                  tools=[{"name": "unknown_tool", "impl": "builtin", "class": "read"}])
        load_skills(str(tmp_path))


def test_user_dir_overrides_builtin(tmp_path):
    builtin = str(tmp_path / "builtin")
    user = str(tmp_path / "user")
    _mk_skill(os.path.join(builtin, "demo"), "skill.demo", description="内置版本")
    _mk_skill(os.path.join(user, "demo"), "skill.demo", description="用户版本")
    skills = load_skills(builtin, user)
    assert skills["skill.demo"].description == "用户版本"


def test_nonexistent_dir_ignored(tmp_path):
    assert load_skills(str(tmp_path / "nope")) == {}


def test_builtin_dir_loads_eight_skills():
    """内置 8 个 SKILL.md 用真实注册工具校验可加载。"""
    from skills.observability import register_observability_skill
    from skills.infra import register_infra_skill
    from skills.rca_skill import register_rca_skill
    from skills.rag_skill import register_rag_skill
    from skills.vm_ops import register_vm_skill
    from skills.alert_ops import register_alert_skill
    from skills.automation import register_automation_skill
    from skills.diagnose import register_diagnose_skill
    for fn in (register_observability_skill, register_infra_skill, register_rca_skill,
               register_rag_skill, register_vm_skill, register_alert_skill,
               register_automation_skill, register_diagnose_skill):
        fn()
    skills = load_skills(builtin_skills_dir())
    assert len(skills) == 8
    assert set(skills) == {"skill.observability", "skill.infra", "skill.rca",
                           "skill.rag_cases", "skill.vm_ops", "skill.alert_ops",
                           "skill.automation", "skill.diagnose"}
