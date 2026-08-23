"""P12 后端 Run API 端点模块（列表/详情/创建）。

前端智能调查中心（InvestigationCenter）数据源：GET/POST /api/v1/ai/runs。
- 列表：返回 Run 摘要（id/status/resource/symptom/created_at 等）。
- 详情：返回单个 Run（含 intent/scope/root_cause/confidence/status）。
- 创建：创建 Run 记录（后续由 run-invocations 触发真实调查链写入）。

数据源：注入的 RunStateStore（In-memory MVP；真实持久化属 P10 后续接 MySQL）。
边界：In-memory；不接真实 query-api/K8s。
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Optional
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException

import contracts
from run_persistence import RunPersistenceError, RunStateStore

router = APIRouter(prefix="/api/v1/ai", tags=["ai-runs"])


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _run_summary(run: contracts.Run) -> dict[str, Any]:
    """Run 摘要（列表用，含前端 InvestigationCenter 所需字段）。"""
    return {
        "run_id": str(run.run_id),
        "request_id": str(run.request_id),
        "status": run.status.value,
        "tenant_id": str(run.tenant_id),
        "primary_cluster_id": str(run.primary_cluster_id) if run.primary_cluster_id else None,
        "intent": run.intent,
        "action_mode": run.action_mode,
        "created_at": run.created_at.isoformat() if run.created_at else None,
    }


def _run_detail(run: contracts.Run) -> dict[str, Any]:
    """Run 详情（详情页用，含 root_cause/confidence/state_version）。"""
    d = _run_summary(run)
    d["state_version"] = getattr(run, "state_version", 0)
    d["root_cause"] = getattr(run, "root_cause", None)
    d["confidence"] = getattr(run, "confidence", None)
    return d


def _get_store() -> RunStateStore:
    from main import _run_state_store
    return _run_state_store


@router.get("/runs")
def list_runs(
    tenant_id: Optional[str] = None,
    store: RunStateStore = Depends(_get_store),
) -> dict[str, Any]:
    """P12：列出 Run（前端调查中心数据源）。可选 tenant_id 过滤。"""
    runs = store.all_runs()
    if tenant_id:
        runs = [r for r in runs if str(r.tenant_id) == tenant_id]
    runs.sort(key=lambda r: r.created_at or datetime.min, reverse=True)
    return {"runs": [_run_summary(r) for r in runs]}


@router.get("/runs/{run_id}")
def get_run(run_id: str, store: RunStateStore = Depends(_get_store)) -> dict[str, Any]:
    """P12：Run 详情。"""
    try:
        run = store.get(UUID(run_id))
    except (ValueError, RunPersistenceError):
        raise HTTPException(status_code=404, detail="RUN_NOT_FOUND")
    return {"run": _run_detail(run)}


@router.post("/runs")
def create_run() -> dict[str, Any]:
    """P10 完整闭环：业务 Run 创建已迁移到 query-api 公共 POST /api/v1/ai/runs。

    边界（评审 P1-2）：orchestrator 不再保留第二公共创建入口。Browser 只连 query-api；
    query-api 作为 Run 持久化 owner 创建 + 写 outbox 可靠派发可信 RunInvocation 给
    orchestrator。此处返回 410 GONE，避免双主。
    """
    raise HTTPException(status_code=410, detail="RUN_CREATION_MOVED_TO_QUERY_API")
