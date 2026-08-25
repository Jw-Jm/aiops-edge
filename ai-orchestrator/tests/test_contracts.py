import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from contracts import (
    ContractValidationError,
    ErrorCode,
    Evidence,
    Hypothesis,
    OpsAction,
    ResourceRef,
    RunControlContext,
    RunInvocationContext,
    ToolResult,
    TrustedRequestContext,
    VerificationResult,
    validate_contract,
)


FIXTURE_PATH = Path(__file__).parents[2] / "docs" / "contracts" / "contract-fixtures.json"


def load_fixture():
    return json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))


# ── Three contexts (V9.2 §11) ──────────────────────────────────────────

def test_three_contexts_validate_from_fixture():
    fixture = load_fixture()
    inv = RunInvocationContext.model_validate(fixture["run_invocation_context"])
    ctrl = RunControlContext.model_validate(fixture["run_control_context"])
    tr = TrustedRequestContext.model_validate(fixture["trusted_request_context"])

    assert inv.context_type == "run_invocation"
    assert ctrl.context_type == "run_control"
    assert ctrl.operation == "cancel"
    assert tr.context_type == "trusted_request"
    assert tr.scope_kind == "cluster"
    assert tr.cluster_id == inv.cluster_scope[0]


def test_trusted_request_cluster_scope_requires_cluster_id():
    base = load_fixture()["trusted_request_context"]
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**base, "cluster_id": None})


def test_trusted_request_run_scope_forbids_cluster_and_non_control_plane():
    base = load_fixture()["trusted_request_context"]
    run_scope = {**base, "scope_kind": "run", "cluster_id": None, "capability": "control_plane.run.read"}
    TrustedRequestContext.model_validate(run_scope)
    # run scope with non-control-plane capability must fail
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**run_scope, "capability": "observability.logs.read"})
    # run scope must not carry cluster_id
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**run_scope, "cluster_id": base["cluster_id"]})


def test_system_principal_requires_null_session():
    base = load_fixture()["run_invocation_context"]
    system = {**base, "principal_type": "system", "principal_id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "session_id": None}
    RunInvocationContext.model_validate(system)
    with pytest.raises(ValidationError):
        RunInvocationContext.model_validate({**system, "session_id": "44444444-4444-4444-8444-444444444444"})


def test_user_principal_requires_session():
    base = load_fixture()["run_invocation_context"]
    with pytest.raises(ValidationError):
        RunInvocationContext.model_validate({**base, "session_id": None})


def test_context_forbids_auth_claims_and_non_uuid_cluster():
    tr = load_fixture()["trusted_request_context"]
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**tr, "roles": ["admin"]})
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**tr, "cluster_id": "prod-sg-01"})


def test_context_lifetime_bounded():
    tr = load_fixture()["trusted_request_context"]
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**tr, "expires_at": "2026-08-19T10:05:00Z"})


def test_trusted_request_workload_kind_is_bounded():
    base = load_fixture()["trusted_request_context"]
    for kind in ("investigation", "chat", "platform"):
        context = TrustedRequestContext.model_validate({**base, "workload_kind": kind})
        assert context.workload_kind == kind
    with pytest.raises(ValidationError):
        TrustedRequestContext.model_validate({**base, "workload_kind": "admin"})


# ── ResourceRef — canonical id does NOT include tenant (V9.2 §10) ──────

def test_resource_ref_does_not_include_tenant_in_id():
    resources = load_fixture()["resources"]
    first = ResourceRef.model_validate(resources[0])
    assert first.resource_id == "service:66666666-6666-4666-8666-666666666666:production:orders"
    assert str(first.tenant_id) == "55555555-5555-4555-8555-555555555555"
    assert str(first.tenant_id) not in first.resource_id


def test_resource_ref_same_name_different_cluster_isolated():
    resources = load_fixture()["resources"]
    first = ResourceRef.model_validate(resources[0])
    second = ResourceRef.model_validate(resources[1])
    assert first.name == second.name == "orders"
    assert first.canonical_id != second.canonical_id


def test_resource_ref_rejects_tenant_in_id_and_slug():
    r0 = load_fixture()["resources"][0]
    with pytest.raises(ValidationError):
        # resource_id with tenant prefix must reject
        ResourceRef.model_validate({**r0, "resource_id": "urn:aiops:55555555-5555-4555-8555-555555555555:66666666-6666-4666-8666-666666666666:service:production:orders"})
    with pytest.raises(ValidationError):
        # slug instead of cluster UUID must reject
        ResourceRef.model_validate({**r0, "resource_id": "service:prod-sg-01:production:orders"})


def test_resource_ref_requires_cluster_and_namespace():
    r0 = load_fixture()["resources"][0]
    with pytest.raises(ValidationError):
        ResourceRef.model_validate({**r0, "cluster_id": "not-a-uuid", "resource_id": "service:not-a-uuid:production:orders"})
    # missing namespace on namespaced resource → use "_", still valid
    without_ns = ResourceRef.model_validate({**r0, "namespace": None, "resource_id": "service:66666666-6666-4666-8666-666666666666:_:orders"})
    assert without_ns.canonical_id == "service:66666666-6666-4666-8666-666666666666:_:orders"


# ── ToolResult — success=true can be no_data (V9.2 §32) ────────────────

def test_tool_result_success_with_data():
    fixture = load_fixture()
    result = ToolResult.model_validate(fixture["tool_result_success"])
    assert result.status == "success"
    assert result.success is True


def test_tool_result_success_empty_is_no_data():
    fixture = load_fixture()
    result = ToolResult.model_validate(fixture["tool_result_empty_no_data"])
    # "executed successfully" and "has data" are distinct
    assert result.success is True
    assert result.status == "no_data"


def test_tool_result_rejects_unknown_status():
    payload = load_fixture()["tool_result_success"] | {"status": "ok"}
    with pytest.raises(ValidationError):
        ToolResult.model_validate(payload)


def test_tool_result_permission_denied_requires_error_code():
    base = load_fixture()["tool_result_success"]
    denied = {**base, "success": False, "status": "permission_denied"}
    with pytest.raises(ValidationError):
        ToolResult.model_validate(denied)
    ok = {**denied, "error_code": "CLUSTER_ACCESS_DENIED"}
    ToolResult.model_validate(ok)


# ── Structured error helper ────────────────────────────────────────────

def test_structured_validation_error_has_stable_code_and_field_paths():
    with pytest.raises(ContractValidationError) as caught:
        validate_contract(RunInvocationContext, {"version": 1})

    error = caught.value
    assert error.error_code == "contract_validation_error"
    assert "tenant_id" in error.fields
    assert all(item["path"] for item in error.fields.values())


# ── Evidence / Verification bounds ─────────────────────────────────────

def test_evidence_claim_type_rules():
    fixture = load_fixture()
    fact = Evidence.model_validate(fixture["evidence"])
    assert fact.claim_type == "fact"
    assert fact.source_reliability == 0.95
    assert fact.provenance_fingerprint

    with pytest.raises(ValidationError):
        Evidence.model_validate({**fixture["evidence"], "claim_type": "knowledge"})
    with pytest.raises(ValidationError):
        Evidence.model_validate({**fixture["evidence"], "source_reliability": 1.1})


def test_verification_window_bounded():
    fixture = load_fixture()
    with pytest.raises(ValidationError):
        VerificationResult.model_validate({**fixture["verification"], "observation_window_seconds": 0})


def test_ops_action_authoritative_risk_not_below_proposed():
    fixture = load_fixture()
    action = OpsAction.model_validate(fixture["ops_action"])
    assert action.authoritative_risk == action.proposed_risk == "R2"
    with pytest.raises(ValidationError):
        OpsAction.model_validate({**fixture["ops_action"], "authoritative_risk": "R0"})


# ── P3.10c-final: CLUSTER_IDENTITY_MISMATCH error code ───────────────────

def test_cluster_identity_mismatch_error_code_present_in_fixture():
    fixture = load_fixture()
    err = fixture["cluster_identity_mismatch_error"]
    assert err["error_code"] == ErrorCode.CLUSTER_IDENTITY_MISMATCH
    assert err["retryable"] is False
    # A binding conflict must not be classified as CLUSTER_UNAVAILABLE.
    assert err["error_code"] != ErrorCode.CLUSTER_UNAVAILABLE


def test_cluster_identity_mismatch_is_distinct_error_code():
    assert ErrorCode.CLUSTER_IDENTITY_MISMATCH != ErrorCode.CLUSTER_UNAVAILABLE
    assert ErrorCode.CLUSTER_IDENTITY_MISMATCH != ErrorCode.RESOURCE_NOT_FOUND
    assert ErrorCode.CLUSTER_IDENTITY_MISMATCH == "CLUSTER_IDENTITY_MISMATCH"
