"""dual_agent: coordinator 目录注入 + spawn_worker 工具 + keyword 兜底（B5）"""
from dual_agent import coordinator_system_prompt, _expert_tools
from persona_registry import load_personas, build_catalog, PERSONAS_BUILTIN_DIR


def test_coordinator_prompt_contains_catalog_and_when_to_use():
    personas = load_personas(PERSONAS_BUILTIN_DIR)
    catalog = build_catalog(personas)
    prompt = coordinator_system_prompt(catalog)
    assert "specialist-sre" in prompt
    assert "specialist-network" in prompt
    assert "incident-investigator" in prompt
    # when_to_use 首行注入
    first = personas["specialist-sre"].when_to_use.strip().splitlines()[0]
    assert first in prompt


def test_coordinator_prompt_excludes_reviewer_reporter():
    personas = load_personas(PERSONAS_BUILTIN_DIR)
    catalog = build_catalog(personas)
    prompt = coordinator_system_prompt(catalog)
    assert "reviewer" not in prompt
    assert "reporter" not in prompt


def test_coordinator_prompt_without_catalog_keeps_base():
    prompt = coordinator_system_prompt("")
    assert "task_type" in prompt


def test_expert_tools_include_spawn_worker():
    """工具集加 spawn_worker：协调器/子 Agent 可派活。"""
    tools = _expert_tools(None, "inspection")
    names = [t.name for t in tools]
    assert "spawn_worker" in names


def test_expert_tools_keyword_fallback_kept():
    """无 expert_registry 时保留 builtin 专家兜底（至少含核心只读工具）。"""
    from skill_registry import ToolRegistry
    if not ToolRegistry.get("query_metrics"):
        ToolRegistry.register(name="query_metrics", description="指标查询", params={},
                              cls_="safe")(lambda service="x": "ok")
    tools = _expert_tools(None, "diagnosis")
    names = [t.name for t in tools]
    assert "query_metrics" in names
