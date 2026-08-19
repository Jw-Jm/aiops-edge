import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from contracts import (
    Evidence,
    Hypothesis,
    OpsAction,
    ContractValidationError,
    RequestContext,
    ResourceRef,
    StructuredError,
    ToolResult,
    VerificationResult,
    validate_contract,
)


FIXTURE_PATH = Path(__file__).parents[2] / "docs" / "contracts" / "contract-fixtures.json"


def load_fixture():
    return json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))


def test_valid_fixture_round_trips_all_core_contracts():
    fixture = load_fixture()

    context = RequestContext.model_validate(fixture["request_context"])
    resource_a = ResourceRef.model_validate(fixture["resources"][0])
    resource_b = ResourceRef.model_validate(fixture["resources"][1])
    tool_result = ToolResult.model_validate(fixture["tool_result"])
    evidence = Evidence.model_validate(fixture["evidence"])
    hypothesis = Hypothesis.model_validate(fixture["hypothesis"])
    action = OpsAction.model_validate(fixture["ops_action"])
    verification = VerificationResult.model_validate(fixture["verification"])

    assert context.cluster_id != resource_b.cluster_id
    assert resource_a.name == resource_b.name == "orders"
    assert resource_a.canonical_id != resource_b.canonical_id
    assert tool_result.status == "success"
    assert evidence.cluster_id == context.cluster_id
    assert hypothesis.supporting_evidence == [evidence.id]
    assert action.target.cluster_id == resource_a.cluster_id
    assert action.target.name == resource_a.name
    assert verification.status == "success"


def test_tool_result_rejects_unknown_status():
    payload = load_fixture()["tool_result"] | {"status": "ok"}

    with pytest.raises(ValidationError):
        ToolResult.model_validate(payload)


def test_request_context_requires_canonical_uuids_and_forbids_auth_claims():
    fixture = load_fixture()["request_context"]

    with pytest.raises(ValidationError):
        RequestContext.model_validate({key: value for key, value in fixture.items() if key != "cluster_id"})

    with pytest.raises(ValidationError):
        RequestContext.model_validate({**fixture, "roles": ["admin"]})

    with pytest.raises(ValidationError):
        RequestContext.model_validate({**fixture, "cluster_id": "prod-sg-01"})


def test_resource_ref_requires_canonical_identity_and_keeps_same_names_isolated():
    resources = load_fixture()["resources"]
    first = ResourceRef.model_validate(resources[0])
    second = ResourceRef.model_validate(resources[1])

    assert first.name == second.name
    assert first.canonical_id != second.canonical_id

    with pytest.raises(ValidationError):
        ResourceRef.model_validate({**resources[0], "resource_id": "service:prod-sg-01:default:orders"})


def test_structured_validation_error_has_stable_code_and_field_paths():
    with pytest.raises(ContractValidationError) as caught:
        validate_contract(RequestContext, {"version": 1})

    error = caught.value
    assert error.error_code == "contract_validation_error"
    assert "cluster_id" in error.fields
    assert all(item["path"] for item in error.fields.values())


def test_evidence_confidence_and_verification_window_are_bounded():
    fixture = load_fixture()

    with pytest.raises(ValidationError):
        Evidence.model_validate({**fixture["evidence"], "confidence": 1.1})

    with pytest.raises(ValidationError):
        VerificationResult.model_validate({**fixture["verification"], "observation_window_seconds": 0})


def test_structured_error_is_safe_to_serialize():
    error = StructuredError(
        error_code="permission_denied",
        message="cluster access denied",
        retryable=False,
        fields={"cluster_id": "not authorized"},
    )

    assert error.model_dump(mode="json")["error_code"] == "permission_denied"
