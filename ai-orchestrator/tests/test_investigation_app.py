import importlib
from pathlib import Path


def test_investigation_entrypoint_selects_stateless_role(monkeypatch):
    monkeypatch.setenv("INVESTIGATION_WORKER_MODE", "false")
    module = importlib.import_module("investigation_app")
    assert module.os.environ["INVESTIGATION_WORKER_MODE"] == "true"
    assert "/internal/v1/run-invocations" in module._ALLOWED_PREFIXES
    source = (Path(module.__file__).with_name("investigation_app.py")).read_text(encoding="utf-8")
    assert "from main" not in source


def test_worker_composition_root_initializes_canonical_query_tools():
    source = Path(__file__).parents[1].joinpath("apps", "investigation.py").read_text(encoding="utf-8")
    assert "init_default_tool_registry()" in source
