import importlib


def test_investigation_entrypoint_selects_stateless_role(monkeypatch):
    monkeypatch.setenv("INVESTIGATION_WORKER_MODE", "false")
    module = importlib.import_module("investigation_app")
    assert module.os.environ["INVESTIGATION_WORKER_MODE"] == "true"
    assert "/internal/v1/run-invocations" in module._ALLOWED_PREFIXES
