"""SkillRegistry 文件化加载 + 热重载测试"""
import os

import pytest
import yaml

from skill_registry import SkillRegistry, ToolRegistry, ToolDef


@pytest.fixture(autouse=True)
def isolate_data_dir(monkeypatch, tmp_path):
    """隔离用户 skills 目录，避免本机 /data 影响断言。"""
    monkeypatch.setenv("AIOPS_DATA_DIR", str(tmp_path / "data"))


def _reset_registry():
    SkillRegistry._skills = {}


def test_init_skills_loads_eight_skills():
    _reset_registry()
    from skills import init_skills
    init_skills()
    names = {s.name for s in SkillRegistry.list_all()}
    assert names == {"skill.observability", "skill.infra", "skill.rca",
                     "skill.rag_cases", "skill.vm_ops", "skill.alert_ops",
                     "skill.automation", "skill.diagnose"}
    assert len(names) == 8


def test_skill_def_fields_mapped_from_file():
    _reset_registry()
    from skills import init_skills
    init_skills()
    s = SkillRegistry.get("skill.rca")
    assert s is not None
    assert s.title == "根因分析"
    assert s.system_prompt.startswith("你擅长故障根因分析")
    assert s.tools == ["rca_analyze"]
    assert "根因" in s.intent_keywords  # activation.keywords 并入 intent_keywords
    assert "根因" in s.activation_keywords


def test_match_ranks_activation_keyword_hits():
    _reset_registry()
    from skills import init_skills
    init_skills()
    hits = SkillRegistry.match("为什么服务响应慢，帮忙定位根因")
    assert hits and hits[0].name == "skill.rca"


def test_match_returns_multiple():
    _reset_registry()
    from skills import init_skills
    init_skills()
    hits = SkillRegistry.match("查看告警事件和指标延迟")
    names = [s.name for s in hits]
    assert "skill.alert_ops" in names
    assert "skill.observability" in names


def test_match_empty_message_returns_empty():
    _reset_registry()
    from skills import init_skills
    init_skills()
    assert SkillRegistry.match("") == []


def test_reload_picks_up_new_skill_file(tmp_path):
    _reset_registry()
    from skills import init_skills
    init_skills()
    assert SkillRegistry.get("skill.reload_demo") is None

    user_skills = tmp_path / "data" / "skills" / "reload-pack"
    os.makedirs(user_skills)
    meta = {
        "name": "skill.reload_demo",
        "description": "热重载演示",
        "when_to_use": "验证 reload 时使用",
        "activation": {"mode": "keyword", "keywords": ["重载演示"]},
        "tools": [{"name": "query_metrics", "impl": "builtin", "class": "read"}],
    }
    text = "---\n" + yaml.safe_dump(meta, allow_unicode=True, sort_keys=False) + "---\n正文"
    with open(os.path.join(user_skills, "SKILL.md"), "w", encoding="utf-8") as f:
        f.write(text)

    SkillRegistry.reload()
    s = SkillRegistry.get("skill.reload_demo")
    assert s is not None and s.description == "热重载演示"
    # 原有 8 个内置 skill 仍在
    assert SkillRegistry.get("skill.rca") is not None


def test_tool_registry_public_api_unchanged():
    """红线: ToolDef/ToolRegistry 公共 API 保持可用。"""
    ToolRegistry.register(name="probe_tmp", description="d", category="test")(lambda: "ok")
    assert ToolRegistry.get("probe_tmp").name == "probe_tmp"
    assert len(ToolRegistry.list_all()) > 0
    desc = ToolRegistry.describe_for_llm()
    assert "probe_tmp" in desc
    t = ToolDef(name="x", description="y", func=lambda: "z")
    assert t.name == "x"
