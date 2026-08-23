"""P11 只读接线 API — propose/list/get/confirm，执行冻结。"""
from __future__ import annotations

from typing import Any, Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from ops_action_hub import ActionNotFoundError, OpsActionHub
from phase11_execution import Phase11Error

router = APIRouter(prefix="/api/v1/ops/actions", tags=["ops-actions"])

_hub = OpsActionHub()


def get_hub() -> OpsActionHub:
    return _hub


class ProposeBody(BaseModel):
    run_id: str
    tenant_id: str
    cluster_id: str
    resource_id: str
    namespace: str = "default"
    action_type: str
    parameters: dict[str, Any] = {}
    expected_effect: str
    verification_policy: str = "manual_check"
    root_cause_confidence: float = 0.0
    resource_version: str = "0"
    rca_status: str
    blast_radius: str = "single_resource"
    environment: str = "production"
    llm_risk_suggestion: str = "R0"


class ConfirmBody(BaseModel):
    requester: str


@router.post("/propose")
def propose(body: ProposeBody):
    try:
        action = get_hub().propose(
            run_id=body.run_id,
            tenant_id=body.tenant_id,
            cluster_id=body.cluster_id,
            resource_id=body.resource_id,
            namespace=body.namespace,
            action_type=body.action_type,
            parameters=body.parameters,
            expected_effect=body.expected_effect,
            verification_policy=body.verification_policy,
            root_cause_confidence=body.root_cause_confidence,
            resource_version=body.resource_version,
            rca_status=body.rca_status,
            blast_radius=body.blast_radius,
            environment=body.environment,
            llm_risk_suggestion=body.llm_risk_suggestion,
        )
    except ActionNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Phase11Error as e:
        raise HTTPException(status_code=400, detail=f"{e.error_code}: {e}")
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    return {"action": action}


@router.get("")
def list_actions(run_id: Optional[str] = None):
    actions = get_hub().list(run_id=run_id)
    return {"actions": actions, "execution_frozen": True}


@router.get("/{action_id}")
def get_action(action_id: str):
    try:
        action = get_hub().get(action_id)
    except ActionNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e))
    return {"action": action}


@router.post("/{action_id}/confirm")
def confirm_action(action_id: str, body: ConfirmBody):
    try:
        result = get_hub().confirm(action_id=action_id, requester=body.requester)
    except ActionNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Phase11Error as e:
        raise HTTPException(status_code=400, detail=f"{e.error_code}: {e}")
    return result
