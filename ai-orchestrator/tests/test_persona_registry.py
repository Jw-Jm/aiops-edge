"""persona 注册表单元测试：load_personas / build_catalog"""
import pytest

from persona_registry import Persona, load_personas, build_catalog, PERSONAS_BUILTIN_DIR

PERSONA_MD = """---
name: specialist-sre
description: 资深 SRE
when_to_use: 涉及 pod 故障
tools: [query_metrics, query_traces]
permission_mode: read-only
max_turns: 20
---
你是资深 SRE。正文。
"""


def test_load_valid_persona(tmp_path):
    d = tmp_path / "personas"
    d.mkdir()
    (d / "specialist-sre.md").write_text(PERSONA_MD, encoding="utf-8")
    personas = load_personas(str(d))
    p = personas["specialist-sre"]
    assert isinstance(p, Persona)
    assert p.when_to_use == "涉及 pod 故障"
    assert p.tools == ["query_metrics", "query_traces"]
    assert p.permission_mode == "read-only"
    assert p.max_turns == 20
    assert "资深 SRE" in p.system_prompt
    assert p.source == "builtin"  # 首个目录视为 builtin


def test_load_ignores_non_md(tmp_path):
    d = tmp_path / "personas"
    d.mkdir()
    (d / "notes.txt").write_text("no frontmatter", encoding="utf-8")
    assert load_personas(str(d)) == {}


def test_missing_when_to_use_raises(tmp_path):
    d = tmp_path / "personas"
    d.mkdir()
    (d / "bad.md").write_text("---\nname: x\ndescription: d\n---\nbody", encoding="utf-8")
    with pytest.raises(ValueError, match="when_to_use"):
        load_personas(str(d))


def test_invalid_permission_mode_raises(tmp_path):
    d = tmp_path / "personas"
    d.mkdir()
    (d / "bad.md").write_text(
        "---\nname: x\nwhen_to_use: w\ntools: []\npermission_mode: sudo\n---\nbody",
        encoding="utf-8")
    with pytest.raises(ValueError, match="permission_mode"):
        load_personas(str(d))


def test_user_dir_overrides_builtin_same_name(tmp_path):
    builtin = tmp_path / "builtin"
    user = tmp_path / "user"
    builtin.mkdir()
    user.mkdir()
    (builtin / "x.md").write_text(PERSONA_MD.replace("specialist-sre", "x"), encoding="utf-8")
    (user / "x.md").write_text(
        "---\nname: x\nwhen_to_use: 用户自定义\n---\nuser body", encoding="utf-8")
    personas = load_personas(str(builtin), str(user))
    assert personas["x"].when_to_use == "用户自定义"
    assert personas["x"].source == "user"


def test_build_catalog_excludes_reviewer_reporter():
    personas = load_personas(PERSONAS_BUILTIN_DIR)
    assert "specialist-sre" in personas
    catalog = build_catalog(personas)
    assert "specialist-sre" in catalog
    assert "incident-investigator" in catalog
    assert "reviewer" not in catalog
    assert "reporter" not in catalog
    # 每行格式 "- name: description | when_to_use 首行"
    assert any(line.startswith("- specialist-sre:") for line in catalog.splitlines())


def test_build_catalog_contains_when_to_use_first_line():
    personas = load_personas(PERSONAS_BUILTIN_DIR)
    catalog = build_catalog(personas)
    first = personas["specialist-sre"].when_to_use.strip().splitlines()[0]
    assert first in catalog
