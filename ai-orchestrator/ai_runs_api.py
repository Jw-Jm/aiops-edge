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
import os

from fastapi import APIRouter, Depends, HTTPException
from fastapi.params import Depends as DependsParam

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


def _get_registry():
    from evidence_registry import get_registry
    return get_registry()


def _load_run_or_404(run_id: str, store: RunStateStore) -> contracts.Run:
    """按 run_id 取 Run；非法 UUID 或不存在 → 404（与 GET /runs/{run_id} 一致）。"""
    try:
        return store.get(UUID(run_id))
    except (ValueError, RunPersistenceError):
        raise HTTPException(status_code=404, detail="RUN_NOT_FOUND")


def _authorize_run_scope(run: contracts.Run, tenant_id: Optional[str], cluster_id: Optional[str]) -> None:
    """tenant+cluster 双参数必须与 Run scope 完全一致；缺失/不匹配均 fail-closed 403。"""
    if not tenant_id or not cluster_id:
        raise HTTPException(status_code=403, detail="SCOPE_MISMATCH")
    if str(run.tenant_id) != tenant_id or str(run.primary_cluster_id or "") != cluster_id:
        raise HTTPException(status_code=403, detail="SCOPE_MISMATCH")


def _legacy_evidence_route_enabled(store: RunStateStore) -> bool:
    """Allow isolated in-memory tests/dev only when no query-api is configured."""
    # A direct Python call still receives FastAPI's Depends sentinel; keep that
    # path retired so authority tests cannot accidentally exercise the legacy
    # store outside an HTTP-injected dev/test route.
    return not bool(os.environ.get("QUERY_API_URL")) and not isinstance(store, DependsParam)


@router.get("/runs/{run_id}/evidences")
def list_run_evidences(
    run_id: str,
    tenant_id: Optional[str] = None,
    cluster_id: Optional[str] = None,
    store: RunStateStore = Depends(_get_store),
) -> dict[str, Any]:
    """Evidence authority moved to query-api; this split-brain route is retired."""
    if _legacy_evidence_route_enabled(store):
        run = _load_run_or_404(run_id, store)
        _authorize_run_scope(run, tenant_id, cluster_id)
        entries = _get_registry().list_evidences(run_id)
        return {"run_id": run_id, "evidences": entries, "count": len(entries)}
    raise HTTPException(status_code=410, detail="EVIDENCE_AUTHORITY_MOVED_TO_QUERY_API")


@router.get("/runs/{run_id}/evidences/{evidence_id}")
def get_run_evidence(
    run_id: str,
    evidence_id: str,
    tenant_id: Optional[str] = None,
    cluster_id: Optional[str] = None,
    store: RunStateStore = Depends(_get_store),
) -> dict[str, Any]:
    """Evidence authority moved to query-api; this split-brain route is retired."""
    if _legacy_evidence_route_enabled(store):
        run = _load_run_or_404(run_id, store)
        _authorize_run_scope(run, tenant_id, cluster_id)
        try:
            evidence = _get_registry().authorize_and_get(run_id, evidence_id, tenant_id, cluster_id)
        except LookupError:
            raise HTTPException(status_code=404, detail="EVIDENCE_NOT_FOUND") from None
        except PermissionError:
            raise HTTPException(status_code=403, detail="SCOPE_MISMATCH") from None
        return {"run_id": run_id, "evidence": evidence}
    raise HTTPException(status_code=410, detail="EVIDENCE_AUTHORITY_MOVED_TO_QUERY_API")


@router.post("/runs")
def create_run() -> dict[str, Any]:
    """P10 完整闭环：业务 Run 创建已迁移到 query-api 公共 POST /api/v1/ai/runs。

    边界（评审 P1-2）：orchestrator 不再保留第二公共创建入口。Browser 只连 query-api；
    query-api 作为 Run 持久化 owner 创建 + 写 outbox 可靠派发可信 RunInvocation 给
    orchestrator。此处返回 410 GONE，避免双主。
    """
    raise HTTPException(status_code=410, detail="RUN_CREATION_MOVED_TO_QUERY_API")
