# flow_api.py
from __future__ import annotations

import json
import os
import re
import uuid
from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel
from flow_engine.store import FlowStore
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry
from flow_engine.nodes_aiops import register_aiops_nodes
from flow_engine.graph import graph_from_dict, validate_graph


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


def _require_internal_token(request: Request):
    """校验请求来自可信的 query-api 代理（已完成 JWT 鉴权并注入内部 token），
    防止绕过代理直连本服务伪造 X-Internal-Role/Approver。与 main.py _require_approver 同源。"""
    expected = os.environ.get("INTERNAL_TOKEN", "")
    got = request.headers.get("X-Internal-Token", "")
    if not expected or got != expected:
        raise HTTPException(403, "请求来源不可信（内部 token 校验失败）")


def _require_admin(request: Request):
    """工作流 CRUD 仅限 admin（P0-3）。"""
    _require_internal_token(request)
    if request.headers.get("X-Internal-Role", "") != "admin":
        raise HTTPException(403, "仅管理员可操作")


class FlowCreate(BaseModel):
    name: str
    description: str = ""
    graph: dict


class FlowUpdate(BaseModel):
    name: str = None
    description: str = None
    graph: dict = None
    enabled: bool = None


class GenerateBody(BaseModel):
    prompt: str


class TestNodeBody(BaseModel):
    type: str
    config: dict = None
    trigger: dict = None


def _strip_code_fences(raw: str) -> str:
    """去掉 ```json 围栏与尾部注释, 提取最外层 JSON 对象。"""
    text = (raw or "").strip()
    m = re.search(r"\{.*\}", text, re.DOTALL)
    return m.group(0) if m else text


class _FlowLLM:
    """复用 orchestrator._llm 同步调用（端点跑在线程池, 不阻塞 event loop）。"""

    def __init__(self, cfg: dict):
        self._cfg = cfg or {}

    def chat(self, system: str, user: str) -> str:
        from orchestrator import _llm
        return _llm(self._cfg, system, user, role="工作流设计器")


def _resolve_llm():
    """按现有 LLM 配置获取方式取 cfg（main._get_brain().llm_config），失败降级 {}。
    api_key 缺失时 orchestrator._llm 会从 _LLM_KEY_HOLDER 回填，与 main.py 模式一致。"""
    cfg = {}
    try:
        from main import _get_brain
        cfg = _get_brain().llm_config or {}
    except Exception:
        pass
    return _FlowLLM(cfg)


GENERATE_SYSTEM_PROMPT = """你是工作流设计器。根据用户需求生成 JSON 对象(不要 markdown 围栏):
{"name":"<短名>","description":"<说明>","graph":{"nodes":[{"id":"n1","type":"trigger.manual","name":"手动触发","config":{},"position":{"x":0,"y":0}}],"edges":[{"id":"e1","source":"n1","sourcePort":"next","target":"n2"}]}}
可用节点类型: {node_types}
规则: 有且仅有一个 trigger.* 节点; 边 sourcePort ∈ next/true/false/approved/rejected/error; 只输出 JSON。"""


@router.get("/node-types")
def list_node_types():
    svc = get_flow_service()
    return {"node_types": svc.node_types()}


@router.post("/generate")
def generate_flow(body: GenerateBody, request: Request):
    _require_admin(request)
    svc = get_flow_service()
    node_types = ", ".join(sorted(t["type"] for t in svc.node_types()))
    raw = _resolve_llm().chat(
        system=GENERATE_SYSTEM_PROMPT.replace("{node_types}", node_types),
        user=body.prompt)
    try:
        graph = json.loads(_strip_code_fences(raw))
    except Exception:
        raise HTTPException(400, "生成结果非合法 JSON")
    if not isinstance(graph, dict) or not isinstance(graph.get("graph"), dict):
        raise HTTPException(400, "生成结果缺少 graph 字段")
    try:
        validate_graph(graph_from_dict(graph["graph"]))
    except ValueError as e:
        raise HTTPException(400, f"生成结果非法: {e}")
    return {"name": graph.get("name", "生成工作流"),
            "description": graph.get("description", ""),
            "graph": graph["graph"]}


@router.post("/test-node")
def test_node(body: TestNodeBody, request: Request):
    """单节点试跑: 构造 1 节点临时图经 Engine.execute 执行, 返回该节点 output。"""
    _require_admin(request)
    svc = get_flow_service()
    if node_registry.lookup(body.type) is None:
        raise HTTPException(400, f"unknown node type: {body.type}")
    graph = graph_from_dict({"nodes": [{"id": "n1", "type": body.type,
                                        "config": body.config or {}, "position": {}}],
                             "edges": []})
    try:
        validate_graph(graph)
    except ValueError as e:
        raise HTTPException(400, str(e))
    result = svc.engine.execute(graph, body.trigger or {})
    nr = result.node_results.get("n1")
    if nr is None:
        raise HTTPException(400, "node did not execute")
    return {"ok": nr.status != "error", "output": nr.output, "error": nr.error}


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
def create_flow(req: FlowCreate, request: Request):
    _require_admin(request)  # P0-3: 工作流定义变更仅限 admin
    svc = get_flow_service()
    try:
        return svc.create_flow(req.name, req.description, req.graph)
    except ValueError as e:
        raise HTTPException(400, str(e))


@router.put("/{flow_id}")
def update_flow(flow_id: str, req: FlowUpdate, request: Request):
    _require_admin(request)  # P0-3: 工作流定义变更仅限 admin
    svc = get_flow_service()
    try:
        return svc.update_flow(flow_id, req.model_dump(exclude_none=True))
    except KeyError:
        raise HTTPException(404, "flow not found")
    except ValueError as e:
        raise HTTPException(400, str(e))


@router.delete("/{flow_id}")
def delete_flow(flow_id: str, request: Request):
    _require_admin(request)  # P0-3: 工作流定义变更仅限 admin
    svc = get_flow_service()
    if not svc.delete_flow(flow_id):
        raise HTTPException(404, "flow not found")
    return {"deleted": flow_id}


@router.post("/{flow_id}/toggle")
def toggle_flow(flow_id: str, request: Request):
    _require_admin(request)  # P0-3: 启停也属工作流定义变更，仅限 admin
    svc = get_flow_service()
    if not svc.toggle_flow(flow_id):
        raise HTTPException(404, "flow not found")
    return svc.get_flow(flow_id)


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
