"""Internal, tenant-scoped historical AI session cleanup contract."""
from __future__ import annotations

import datetime as _datetime
import hmac
import os
from typing import Any, Callable

from fastapi import APIRouter, HTTPException, Request

router = APIRouter(prefix="/internal/v1/data-cleanups", tags=["data-cleanups"])


def _default_session_store() -> Any:
    """Load the legacy SQLite adapter only when an internal cleanup is invoked.

    Production Gateway imports this module while publishing its route table.  A
    top-level ``SessionStore()`` would create/open the legacy SQLite file for
    every Gateway pod, even though normal production traffic never uses this
    migration endpoint.  Keeping the import behind the explicit internal
    operation preserves the cleanup contract without making SQLite a startup
    dependency or a second Chat owner.
    """

    from session_store import session_store

    return session_store


_session_store_factory: Callable[[], Any] = _default_session_store


def configure_data_cleanup_runtime(
    brain_getter=None, session_store_factory: Callable[[], Any] | None = None
):
    """Inject test/runtime dependencies without exposing SQLite to query-api."""
    del brain_getter  # reserved for future checkpointer-specific health reporting
    global _session_store_factory
    if session_store_factory is not None:
        _session_store_factory = session_store_factory


def _require_internal_cleanup_token(request: Request) -> None:
    expected = os.environ.get("QUERY_TO_ORCHESTRATOR_TOKEN", "")
    supplied = request.headers.get("X-Internal-Token", "")
    if not expected or not supplied or not hmac.compare_digest(supplied, expected):
        raise HTTPException(status_code=401, detail="unauthorized")


def _parse_cutoff(value: object) -> float:
    raw = str(value or "").strip()
    if not raw:
        raise HTTPException(status_code=400, detail="cutoff_at is required")
    try:
        parsed = _datetime.datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError as exc:
        raise HTTPException(status_code=400, detail="cutoff_at must be RFC3339") from exc
    if parsed.tzinfo is None:
        raise HTTPException(status_code=400, detail="cutoff_at timezone is required")
    cutoff = parsed.astimezone(_datetime.timezone.utc)
    if cutoff > _datetime.datetime.now(_datetime.timezone.utc):
        raise HTTPException(status_code=400, detail="cutoff_at cannot be in the future")
    return cutoff.timestamp()


@router.post("/ai-sessions")
def cleanup_ai_sessions(body: dict, request: Request):
    _require_internal_cleanup_token(request)
    tenant_id = str(body.get("tenant_id") or "").strip()
    if not tenant_id:
        raise HTTPException(status_code=400, detail="tenant_id is required")
    cluster_id = str(body.get("cluster_id") or "").strip()
    cutoff_epoch = _parse_cutoff(body.get("cutoff_at"))
    is_preview = bool(body.get("preview"))
    if not is_preview:
        operation_id = str(body.get("operation_id") or "").strip()
        request_digest = str(body.get("request_digest") or "").strip()
        if not operation_id:
            raise HTTPException(status_code=400, detail="operation_id is required")
        if not request_digest:
            raise HTTPException(status_code=400, detail="request_digest is required")
        if request.headers.get("X-Cleanup-Operation-Id", "") != operation_id:
            raise HTTPException(status_code=403, detail="operation_id header mismatch")
        if request.headers.get("X-Cleanup-Request-Digest", "") != request_digest:
            raise HTTPException(status_code=403, detail="request_digest header mismatch")

    store = _session_store_factory()
    if is_preview:
        return {
            "scope": "ai_sessions",
            "table": "sessions",
            "estimated_rows": store.count_before(cutoff_epoch, tenant_id, cluster_id),
        }
    return store.delete_before(cutoff_epoch, tenant_id, cluster_id)
