import pytest

from main import build_action_candidate


def test_action_candidate_requires_structured_target():
    with pytest.raises(ValueError, match="ACTION_PREFLIGHT_REQUIRED"):
        build_action_candidate({"script": "kubectl scale deployment/orders --replicas=2"})


def test_action_candidate_keeps_only_supported_semantic_fields():
    candidate = build_action_candidate({
        "namespace": "prod",
        "target_name": "orders",
        "operation": "scale",
        "params": {"replicas": 2},
        "script": "ignored presentation text",
    })
    assert candidate == {
        "resource_type": "deployment",
        "namespace": "prod",
        "target_name": "orders",
        "operation": "scale",
        "params": {"replicas": 2},
    }
