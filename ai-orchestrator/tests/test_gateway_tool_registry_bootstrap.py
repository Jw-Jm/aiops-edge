"""Gateway composition must load the canonical internal-query Tool registry."""

from pathlib import Path


def test_gateway_composition_initializes_canonical_query_tools():
    source = Path(__file__).parents[1].joinpath("orchestrator.py").read_text(encoding="utf-8")
    assert "from tool_registry import init_default_tool_registry" in source
    assert "init_default_tool_registry()" in source
