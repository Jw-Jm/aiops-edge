# ai-orchestrator/tests/test_rbac_toggle.py
import os


def test_scripts_exist():
    base = os.path.join(os.path.dirname(__file__), "..", "..", "deploy", "scripts")
    assert os.path.exists(os.path.join(base, "grant-orchestrator-ops.sh"))
    assert os.path.exists(os.path.join(base, "revoke-orchestrator-ops.sh"))
