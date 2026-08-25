from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


def test_canonical_workflow_has_single_durable_owners():
    action_control = (ROOT / "ai-apm-query-go/internal/api/action_control.go").read_text()
    decision = (ROOT / "ai-apm-query-go/internal/api/action_decision.go").read_text()
    assert "/decision" in action_control
    assert "FOR UPDATE" in decision
    assert "ai_action_outbox" in decision
    assert "X-Executor-Signature" not in decision  # query-api signs only at executor boundary
