from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


def test_reconcile_is_truthful_and_not_synthetic():
    source = (ROOT / "ai-action-executor/main.go").read_text()
    assert "readReconcileStateFn" in source
    assert 'status := "not_applied"' in source
    assert "target state already matches" in source
    assert 'Status: "reconciled"' not in source
