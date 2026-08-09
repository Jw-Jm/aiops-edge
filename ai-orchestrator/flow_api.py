# flow_api.py
from __future__ import annotations

import json
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel
from flow_engine.store import FlowStore
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry
from flow_engine.nodes_aiops import register_aiops_nodes


# 用户自定义工作流 CRUD 使用 /api/v1/ai/workflows，避免与内置 DAG 描述
# 端点 /api/v1/ai/flows（main.py 的 ai_flows/ai_flow_detail）冲突遮蔽。
router = APIRouter(prefix="/api/v1/ai/workflows")
_service = None


def set_flow_service(svc: WorkflowService):
    global _service
    _service = svc


def get_flow_service() -> WorkflowService:
    global _service
    if _service is None:
        register_aiops_nodes()
        _service = WorkflowService(FlowStore())
    return _service


class FlowCreate(BaseModel):
    name: str
    description: str = ""
    graph: dict


class FlowUpdate(BaseModel):
    name: str = None
    description: str = None
    graph: dict = None
    enabled: bool = None


class RunRequest(BaseModel):
    trigger: dict = None
    message: str = ""
    service: str = ""


class ResumeRequest(BaseModel):
    approved: bool = True


@router.get("/node-types")
def list_node_types():
    svc = get_flow_service()
    return {"node_types": svc.node_types()}


@router.get("")
def list_flows():
    svc = get_flow_service()
    return {"flows": svc.list_flows()}


@router.get("/{flow_id}")
def get_flow(flow_id: str):
    svc = get_flow_service()
    f = svc.get_flow(flow_id)
    if not f:
        raise HTTPException(404, "flow not found")
    return f


@router.post("", status_code=201)
def create_flow(req: FlowCreate):
    svc = get_flow_service()
    try:
        return svc.create_flow(req.name, req.description, req.graph)
    except ValueError as e:
        raise HTTPException(400, str(e))


@router.put("/{flow_id}")
def update_flow(flow_id: str, req: FlowUpdate):
    svc = get_flow_service()
    try:
        return svc.update_flow(flow_id, req.model_dump(exclude_none=True))
    except KeyError:
        raise HTTPException(404, "flow not found")
    except ValueError as e:
        raise HTTPException(400, str(e))


@router.delete("/{flow_id}")
def delete_flow(flow_id: str):
    svc = get_flow_service()
    if not svc.delete_flow(flow_id):
        raise HTTPException(404, "flow not found")
    return {"deleted": flow_id}


@router.post("/{flow_id}/toggle")
def toggle_flow(flow_id: str):
    svc = get_flow_service()
    if not svc.toggle_flow(flow_id):
        raise HTTPException(404, "flow not found")
    return svc.get_flow(flow_id)


@router.post("/{flow_id}/run")
def run_flow(flow_id: str, req: RunRequest):
    svc = get_flow_service()
    trigger = req.trigger or {}
    if req.service:
        trigger.setdefault("service", req.service)
    try:
        run_id = f"run_{flow_id}_{abs(hash(flow_id + str(trigger))) % 10**10}"
        result = svc.run_flow(flow_id, trigger, run_id)
        return {"run_id": run_id, "status": result.status,
                "result": result.context.nodes.get("summarize", {}).get("output", {}) if result.status == "succeeded" else {}}
    except KeyError:
        raise HTTPException(404, "flow not found")
    except ValueError as e:
        raise HTTPException(400, str(e))


@router.get("/{flow_id}/runs")
def list_runs(flow_id: str):
    svc = get_flow_service()
    return {"runs": svc.store.list_runs(flow_id)}


@router.get("/{flow_id}/runs/{run_id}")
def get_run(flow_id: str, run_id: str):
    svc = get_flow_service()
    run = svc.store.get_run(run_id)
    if not run:
        raise HTTPException(404, "run not found")
    run["nodes"] = svc.store.get_run_nodes(run_id)
    return {"run": run}


@router.post("/{flow_id}/runs/{run_id}/resume")
def resume_run(flow_id: str, run_id: str, req: ResumeRequest):
    svc = get_flow_service()
    try:
        result = svc.resume_run(run_id, req.approved)
        return {"run_id": run_id, "status": result.status}
    except KeyError:
        raise HTTPException(404, "run not found")
