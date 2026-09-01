"""Stateless Investigation Worker composition root.

This module intentionally does not import :mod:`main`.  The worker owns only
the signed RunInvocation ingress, bounded dispatcher, durable control-plane
recovery loop, health and metrics endpoints.  Browser chat, legacy routes,
APScheduler jobs and the legacy SQLite session store stay in the gateway
process.
"""
from __future__ import annotations

import asyncio
import os
import secrets
import uuid
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse, PlainTextResponse

from authorization_matrix import AuthzError, build_runtime_authorization_matrix
from control_plane_client import ControlPlaneClient
from error_safety import stable_error_code
from internal_ingress import build_invocation_scope, verify_run_invocation_ingress
from investigation_dispatcher import AcceptedInvocation, InvestigationDispatcher
from investigation_runtime import InvestigationRuntime
from tool_registry import init_default_tool_registry

# The gateway used to initialize the registry as a side effect of its startup.
# This worker has an independent composition root, so initialize the canonical
# read-only Tool definitions here as well; otherwise every signed graph/evidence
# query fails the local Tool gate with ``invalid_context`` before reaching
# query-api.  Registration is idempotent and does not execute a tool.
init_default_tool_registry()

# Set before importing orchestrator.  BrainOrchestrator uses this flag to
# disable its SQLite checkpoint/session store during module construction.
os.environ["INVESTIGATION_WORKER_MODE"] = "true"
from orchestrator import brain  # noqa: E402


_AUTH_ALLOWLIST = ("/health", "/readyz", "/metrics")
_TERMINAL = {"success", "partial", "failed", "cancelled", "regressed"}


def _public_path_allowed(path: str) -> bool:
    """Health/metrics probes are public only at their exact route."""

    return path in _AUTH_ALLOWLIST


def _build_authz_matrix():
    raw = os.environ.get("SERVICE_ACCOUNT_ROLES", "")
    roles = {}
    if raw:
        try:
            import json
            roles = json.loads(raw)
        except Exception:  # noqa: BLE001 - malformed mapping remains fail-closed
            roles = {}
    return build_runtime_authorization_matrix(roles)[1]


_authz_matrix = _build_authz_matrix()
_dispatcher: InvestigationDispatcher | None = None
_recovery_task: asyncio.Task | None = None


class _WorkerBrain:
    """Adapt the existing graph brain to the durable InvestigationRuntime.

    The graph brain is imported directly (without the gateway composition
    root), so the worker retains its diagnostic behavior while remaining free
    of browser routes and gateway schedulers.
    """

    async def investigate(self, item: AcceptedInvocation, lease) -> dict:
        events: list[dict] = []
        status = "success"
        error_code = ""
        final_text = ""
        saw_done = False

        if item.request_context is None:
            raise RuntimeError("RUN_CONTEXT_REQUIRED")

        # Run the strict RCA V2 first.  It is deliberately optional at runtime:
        # an unavailable graph/evidence source yields a truthful partial result,
        # never a fabricated success.
        try:
            from rca_engine import RCARequest
            from rca_engine.engine import RCAEngineV2
            from rca_engine.runtime import (
                InvestigationEvidenceProvider,
                InvestigationGraphClient,
                persist_graph_context,
            )

            if not item.window_start or not item.window_end or not item.symptom_time:
                raise ValueError("RUN_TIME_RANGE_REQUIRED")
            ai_run = {
                "run_id": item.run_id,
                "tenant_id": item.tenant_id,
                "primary_cluster_id": item.cluster_id,
                "target_type": item.target_type,
                "target_resource_id": item.resource_id,
                "time_range_start": item.window_start,
                "time_range_end": item.window_end,
                "symptom_time": item.symptom_time,
            }
            request = RCARequest.from_ai_run(
                ai_run,
                resource_id=item.resource_id,
                entity_name=item.service or item.resource_id,
                symptoms=(item.intent or "diagnosis",),
            )
            control_plane = ControlPlaneClient()

            def persist(result_payload, context_payload):
                return persist_graph_context(
                    control_plane,
                    result=result_payload,
                    context=context_payload,
                    run_id=item.run_id,
                    tenant_id=item.tenant_id,
                    cluster_id=item.cluster_id,
                )

            rca_result = await asyncio.to_thread(
                RCAEngineV2(
                    graph_client=InvestigationGraphClient(item),
                    evidence_provider=InvestigationEvidenceProvider(item),
                    persistence=persist,
                ).diagnose,
                request,
                item.request_context,
            )
            events.append({
                "type": "rca.v2",
                "event_type": "rca.v2",
                "status": rca_result.root_cause_status,
                "root_cause": rca_result.root_cause,
                "confidence": rca_result.confidence,
                "propagation_paths": rca_result.propagation_paths,
                "window_start": rca_result.window_start,
                "window_end": rca_result.window_end,
                "symptom_time": rca_result.symptom_time,
                "graph_enhanced": rca_result.graph_enhanced,
                "graph_warning_codes": list((rca_result.graph_context or {}).get("warning_codes", [])),
                "graph_partial": bool((rca_result.graph_context or {}).get("partial", False)),
                "graph_stale": bool((rca_result.graph_context or {}).get("stale", False)),
            })
            if not rca_result.graph_enhanced:
                status, error_code = "partial", "GRAPH_UNAVAILABLE"
            elif rca_result.root_cause_status == "insufficient_evidence":
                status, error_code = "partial", "INSUFFICIENT_EVIDENCE"
        except Exception as exc:  # noqa: BLE001 - explicit partial RCA
            events.append({
                "type": "rca.error",
                "event_type": "rca.error",
                "error_code": "RCA_V2_UNAVAILABLE",
                "status": "partial",
            })
            status, error_code = "partial", "RCA_V2_UNAVAILABLE"

        if item.action_mode == "read_only":
            saw_done = True
        else:
            async for event in brain.stream_sync(
                item.intent or "diagnosis",
                item.service or "",
                item.message or "",
                item.invocation_id,
                mode="full",
                request_context=item.request_context,
            ):
                lease.check_active()
                events.append(event)
                if isinstance(event, dict):
                    event_type = str(event.get("type") or event.get("event_type") or "")
                    if event_type == "error":
                        status, error_code = "failed", stable_error_code(
                            event.get("error_code") or event.get("error"), "BRAIN_ERROR")
                    elif event_type == "tool_end" and str(event.get("status") or "").lower() in {"failed", "unavailable"}:
                        status, error_code = "failed", stable_error_code(
                            event.get("error_code") or event.get("error"), "TOOL_FAILED")
                    elif event_type == "done":
                        saw_done = True
                        final_text = str(event.get("text") or "")

        if status == "success" and (not saw_done or not final_text.strip()) and item.action_mode != "read_only":
            status, error_code = "partial", error_code or ("INCOMPLETE_STREAM" if not saw_done else "NO_DATA")
        result = {"events": len(events)}
        if final_text:
            result["report"] = final_text
        if error_code:
            result["error_code"] = error_code
        return {"status": status, "events": events, "error_code": error_code, "result": result}


def _runtime_enabled() -> bool:
    return os.environ.get("INVESTIGATION_RUNTIME_ENABLED", "1").lower() in {"1", "true", "yes", "on"}


async def _get_dispatcher() -> InvestigationDispatcher:
    global _dispatcher
    if _dispatcher is None:
        runtime = InvestigationRuntime(brain=_WorkerBrain())
        _dispatcher = InvestigationDispatcher(runtime, capacity=max(1, int(os.environ.get("INVESTIGATION_QUEUE_CAPACITY", "100"))))
        await _dispatcher.start(workers=max(1, int(os.environ.get("INVESTIGATION_WORKERS", "1"))))
    return _dispatcher


async def _recover() -> None:
    if not os.environ.get("QUERY_API_URL"):
        return
    try:
        from invocation_scope import InvocationScope

        tenant_id = os.environ.get("AIOPS_SYSTEM_TENANT_ID", "").strip()
        if not tenant_id:
            return
        runs = await asyncio.to_thread(
            ControlPlaneClient().list_unfinished,
            tenant_id=tenant_id,
            worker_kind="investigation",
        )
        items = []
        for run in runs:
            if str(run.get("status") or "") not in {"created", "planning", "investigating", "verifying"}:
                continue
            run_id = str(run.get("run_id") or "")
            cluster_id = str(run.get("cluster_id") or run.get("primary_cluster_id") or "")
            request_id = str(run.get("request_id") or "")
            invocation_id = str(run.get("invocation_id") or "")
            run_tenant_id = str(run.get("tenant_id") or "")
            if not all((run_id, cluster_id, request_id, invocation_id, run_tenant_id)):
                continue
            scope = InvocationScope(
                principal_type="system",
                principal_id="f4a4b8c2-3d5e-4f6a-8b9c-0d1e2f3a4b5c",
                session_id=None,
                tenant_id=run_tenant_id,
                cluster_id=cluster_id,
                request_id=request_id,
                source="recovery",
                run_id=run_id,
                invocation_id=invocation_id,
                workload_kind="investigation",
            )
            items.append(AcceptedInvocation(
                run_id=run_id,
                invocation_id=invocation_id,
                request_id=request_id,
                tenant_id=run_tenant_id,
                cluster_id=cluster_id,
                intent=str(run.get("intent") or "diagnosis"),
                resource_id=str(run.get("target_resource_id") or ""),
                service=str(run.get("target_resource_id") or ""),
                message=str(run.get("intent") or "对目标进行诊断"),
                action_mode=str(run.get("action_mode") or "read_only"),
                request_context=scope,
                target_type=str(run.get("target_type") or "service"),
                window_start=str(run.get("time_range_start") or ""),
                window_end=str(run.get("time_range_end") or ""),
                symptom_time=str(run.get("symptom_time") or run.get("time_range_end") or ""),
            ))
        if items:
            await (await _get_dispatcher()).recover(items)
    except Exception:
        # Recovery is retried by the loop; the durable control plane remains
        # authoritative and no data-plane query is performed here.
        return


async def _recovery_loop() -> None:
    interval = max(1, int(os.environ.get("INVESTIGATION_RECOVERY_INTERVAL_SECONDS", "30")))
    while True:
        await _recover()
        await asyncio.sleep(interval)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global _recovery_task
    if _runtime_enabled() and os.environ.get("QUERY_API_URL"):
        _recovery_task = asyncio.create_task(_recovery_loop())
    await _get_dispatcher()
    try:
        yield
    finally:
        if _recovery_task is not None:
            _recovery_task.cancel()
            await asyncio.gather(_recovery_task, return_exceptions=True)
            _recovery_task = None
        if _dispatcher is not None:
            await _dispatcher.stop()


app = FastAPI(title="AIOps Investigation Worker", version="5.0", lifespan=lifespan)


@app.middleware("http")
async def auth_middleware(request: Request, call_next):
    if _public_path_allowed(request.url.path):
        return await call_next(request)
    expected = os.environ.get("QUERY_TO_ORCHESTRATOR_TOKEN") or os.environ.get("INTERNAL_TOKEN", "")
    provided = request.headers.get("X-Internal-Token", "")
    if not expected or not provided or not secrets.compare_digest(provided, expected):
        return JSONResponse({"error": "authentication required"}, status_code=401)
    return await call_next(request)


@app.get("/health")
@app.get("/readyz")
async def ready():
    if not _runtime_enabled():
        return JSONResponse({"status": "unavailable", "reason": "investigation_runtime_disabled"}, status_code=503)
    return {"status": "ok"}


@app.get("/metrics")
async def metrics_endpoint():
    from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
    return PlainTextResponse(generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/internal/v1/run-invocations")
async def run_invocation(request: Request):
    claims = verify_run_invocation_ingress(request)
    if str(claims.get("capability") or "") != "ai.investigate":
        raise HTTPException(status_code=403, detail="CAPABILITY_DENIED")
    signed_run_id = str(claims.get("run_id") or "")
    signed_invocation_id = str(claims.get("invocation_id") or "")
    if not signed_run_id or not signed_invocation_id:
        raise HTTPException(status_code=403, detail="RUN_IDENTITY_REQUIRED")
    body = await request.json() or {}
    if str(body.get("run_id") or "") != signed_run_id or str(body.get("invocation_id") or "") != signed_invocation_id:
        raise HTTPException(status_code=403, detail="RUN_IDENTITY_MISMATCH")
    try:
        _authz_matrix.authorize(
            principal=str(claims.get("principal_id") or ""),
            tenant_id=str(claims.get("tenant_id") or ""),
            cluster_id=str((claims.get("cluster_scope") or [""])[0]),
            capability="ai.investigate",
            action="create",
            risk="R0",
        )
    except AuthzError as exc:
        raise HTTPException(status_code=403, detail=str(exc)) from exc
    scope = build_invocation_scope(claims)
    if body.get("tenant_id") and str(body["tenant_id"]) != scope.tenant_id:
        raise HTTPException(status_code=403, detail="TENANT_ACCESS_DENIED")
    if body.get("cluster_id") and str(body["cluster_id"]) != scope.cluster_id:
        raise HTTPException(status_code=403, detail="CLUSTER_ACCESS_DENIED")
    if not _runtime_enabled() or not os.environ.get("QUERY_API_URL"):
        raise HTTPException(status_code=503, detail="CONTROL_PLANE_REQUIRED")
    try:
        accepted = await (await _get_dispatcher()).accept(AcceptedInvocation(
            run_id=signed_run_id,
            invocation_id=signed_invocation_id,
            request_id=str(claims.get("request_id") or ""),
            tenant_id=scope.tenant_id,
            cluster_id=scope.cluster_id,
            intent=str(body.get("intent") or "diagnosis"),
            resource_id=str(body.get("resource_id") or body.get("service") or ""),
            service=str(body.get("service") or body.get("resource_id") or ""),
            message=str(body.get("message") or body.get("intent") or "对目标进行诊断"),
            action_mode=str(body.get("action_mode") or "read_only"),
            request_context=scope,
            target_type=str(body.get("target_type") or "service"),
            window_start=str(body.get("time_range_start") or ""),
            window_end=str(body.get("time_range_end") or ""),
            symptom_time=str(body.get("symptom_time") or body.get("time_range_end") or ""),
        ))
    except asyncio.QueueFull as exc:
        raise HTTPException(status_code=429, detail="INVESTIGATION_QUEUE_FULL") from exc
    return JSONResponse(status_code=202, content={
        "run_id": accepted.run_id,
        "invocation_id": accepted.invocation_id,
        "accepted": accepted.accepted,
        "duplicate": accepted.duplicate,
    })
