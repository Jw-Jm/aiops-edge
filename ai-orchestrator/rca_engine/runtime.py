"""Production adapters for the versioned RCA engine.

This module is deliberately small and boring: an Investigation worker gets a
graph/evidence client that can only use ``InternalQueryClient``.  It must not
open HugeGraph, ClickHouse, Kubernetes or a local cache directly.
"""

from __future__ import annotations

import json
import os
import uuid
from typing import Any, Mapping

from internal_query import _load_private_key
from internal_query_client import InternalQueryClient
from tool_execution_context import ToolExecutionContext
from trusted_context_issuer import TrustedContextIssuer


def _client() -> InternalQueryClient:
    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    return InternalQueryClient(issuer=TrustedContextIssuer(private_key=private_key))


def _execution_context(item: Any, *, tool_id: str, params: Mapping[str, Any]) -> ToolExecutionContext:
    scope = item.request_context
    raw_params = json.dumps(dict(params), ensure_ascii=False, sort_keys=True, separators=(",", ":"), default=str)
    try:
        namespace = uuid.UUID(str(item.run_id))
    except (ValueError, AttributeError):
        namespace = uuid.NAMESPACE_URL
    tool_run_id = str(uuid.uuid5(namespace, f"aiops:tool:{tool_id}:{raw_params}"))
    return ToolExecutionContext.from_mapping(
        {
            "workload_kind": "investigation",
            "run_id": str(item.run_id),
            "invocation_id": str(item.invocation_id),
            "tenant_id": str(item.tenant_id),
            "cluster_id": str(item.cluster_id),
            "executor_id": str(getattr(scope, "executor_id", "") or ""),
            "lease_epoch": int(getattr(scope, "lease_epoch", 0) or 0),
            "lease_token": str(getattr(scope, "lease_token", "") or ""),
            "query_window_start": str(getattr(item, "window_start", "") or ""),
            "query_window_end": str(getattr(item, "window_end", "") or ""),
            "tool_run_id": tool_run_id,
            "idempotency_key": f"{item.run_id}:{tool_run_id}",
        },
        tool_id=tool_id,
        params=params,
    )


class InvestigationGraphClient:
    """Callable graph adapter consumed by :class:`RCAEngineV2`."""

    def __init__(self, item: Any):
        self.item = item
        self.client = _client()
        self.failures: list[str] = []

    def __call__(self, **params: Any) -> dict[str, Any]:
        operation = str(params.pop("graph_operation", "") or "")
        if not operation:
            raise ValueError("graph_operation is required")
        execution = _execution_context(
            self.item, tool_id="query_graph.v1", params={"graph_operation": operation, **params}
        )
        result = self.client.query_graph_v1(
            tenant_id=str(self.item.tenant_id), cluster_id=str(self.item.cluster_id),
            params={"graph_operation": operation, **params},
            context_ref=str(self.item.request_id), execution_context=execution,
        )
        return result.body


def _items(body: Mapping[str, Any], *keys: str) -> list[dict[str, Any]]:
    data = body.get("data") if isinstance(body.get("data"), Mapping) else body
    for key in keys:
        value = data.get(key) if isinstance(data, Mapping) else None
        if isinstance(value, list):
            return [dict(item) for item in value if isinstance(item, Mapping)]
    return []


def _candidate_names(context: Any) -> list[str]:
    names = []
    for vertex in list(getattr(context, "vertices", []) or [])[:12]:
        name = str(vertex.get("name") or vertex.get("entity_uid") or "").strip()
        if name and name not in names:
            names.append(name)
    return names[:12]


class InvestigationEvidenceProvider:
    """Collect bounded, typed evidence through query-api internal tools.

    The provider never invents a severity/timestamp.  If a datasource is
    unavailable, it records no evidence and lets the deterministic engine
    return ``insufficient_evidence``/``partial`` with the missing category.
    """

    def __init__(self, item: Any):
        self.item = item
        self.client = _client()

    def _query(self, tool_id: str, operation: str, params: dict[str, Any]) -> dict[str, Any]:
        execution = _execution_context(self.item, tool_id=tool_id, params=params)
        body = self.client.query(
            tool_id=tool_id, operation=operation,
            tenant_id=str(self.item.tenant_id), cluster_id=str(self.item.cluster_id),
            params=params, context_ref=str(self.item.request_id),
            execution_context=execution,
        ).body
        # Query-api owns ToolRun and ai_evidence persistence.  Consume each
        # eligible result immediately; keeping evidence only in this Python
        # process would make RCA non-replayable after worker restart.
        tool_run_id = str(body.get("tool_run_id") or "")
        if tool_run_id and body.get("quality") in {"complete", "partial"}:
            from control_plane_client import ControlPlaneClient
            try:
                evidence_id = str(uuid.uuid5(uuid.UUID(str(self.item.run_id)), tool_run_id))
            except (ValueError, AttributeError):
                evidence_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"{self.item.run_id}:{tool_run_id}"))
            raw = json.dumps(body.get("data", body), ensure_ascii=False, sort_keys=True, default=str)
            digest = str(body.get("digest") or "")
            ControlPlaneClient().consume_tool_evidence(
                run_id=str(self.item.run_id), tenant_id=str(self.item.tenant_id),
                cluster_id=str(self.item.cluster_id), tool_run_id=tool_run_id,
                evidence_id=evidence_id, evidence_type=operation,
                source_ref=f"query-api:{operation}", raw_ref=tool_run_id,
                raw_digest_sha256=digest, summary=raw[:4000],
                metadata={"quality": body.get("quality"), "count": body.get("count", 0)},
                provenance_fingerprint=digest,
            )
        return body

    def _append(self, output: list[dict[str, Any]], category: str, body: Mapping[str, Any],
                candidates: list[dict[str, Any]]) -> None:
        rows = _items(body, category, "points", "traces", "logs", "alerts", "changes", "events", "items")
        for row in rows:
            item = dict(row)
            # Preserve provider timestamps only when present.  The RCA scorer
            # computes temporal score from these fields and ignores any
            # provider-supplied ``temporal_score``.
            item["category"] = category
            uid = str(item.get("entity_uid") or item.get("resource_id") or "")
            service_name = str(item.get("service_name") or item.get("ServiceName") or item.get("service") or item.get("Service") or "")
            if not uid and service_name:
                for candidate in candidates:
                    candidate_uid = str(candidate.get("entity_uid") or "")
                    candidate_name = str(candidate.get("name") or candidate_uid)
                    if service_name in {candidate_uid, candidate_name}:
                        uid = candidate_uid
                        break
            if not uid and len(candidates) == 1:
                uid = str(candidates[0].get("entity_uid") or "")
            if uid:
                item["entity_uid"] = uid
            observed = next((item.get(key) for key in ("observed_at", "timestamp", "occurred_at", "event_time", "detected_at", "t", "T", "Timestamp", "Start", "StartTime", "LastTS", "last_timestamp") if item.get(key) is not None), None)
            if observed is not None:
                item["observed_at"] = str(observed)
            if category == "metric" and "severity" not in item:
                calls = item.get("call_count", item.get("CallCount", 0)) or 0
                errors = item.get("error_count", item.get("ErrorCount", 0)) or 0
                try:
                    item["severity"] = min(1.0, max(0.0, float(errors) / max(float(calls), 1.0)))
                except (TypeError, ValueError):
                    item["severity"] = 0.0
            if category == "alert" and "severity" in item and isinstance(item["severity"], str):
                item["severity"] = {"critical": 1.0, "fatal": 1.0, "error": 1.0, "warning": .6, "warn": .6, "info": .2}.get(item["severity"].lower(), 0.0)
            output.append(item)

    def __call__(self, request: Any, context: Any) -> list[dict[str, Any]]:
        names = _candidate_names(context)
        if not names:
            return []
        candidates = [v for v in list(context.vertices or [])[:12] if isinstance(v, Mapping)]
        output: list[dict[str, Any]] = []
        # One bounded call per candidate/domain keeps the automatic read
        # budget bounded (<= 20) even when the graph returns many candidates.
        # MetricsRepository returns a single-service RED series. Keep one
        # bounded ToolRun per candidate (at most 12) so every point retains an
        # unambiguous entity UID; the four batched domains bring the total to
        # at most 16 read calls per investigation.
        for candidate in candidates:
            name = str(candidate.get("name") or candidate.get("entity_uid") or "")
            if not name:
                continue
            try:
                self._append(output, "metric", self._query("query_metrics.v1", "metrics", {"service": name}), [candidate])
            except Exception as exc:
                self.failures.append(f"metrics:{name}:{str(exc)[:100]}")
        calls = [
            ("query_traces.v1", "traces", {"services": names, "limit": 100}, "trace"),
            ("query_logs.v1", "logs", {"services": names, "limit": 100}, "log"),
            ("query_alerts.v1", "alerts", {"services": names, "limit": 100}, "alert"),
            ("query_changes.v1", "changes", {"services": names}, "change"),
        ]
        for tool_id, operation, params, category in calls:
            try:
                self._append(output, category, self._query(tool_id, operation, params), candidates)
            except Exception as exc:
                self.failures.append(f"{operation}:{str(exc)[:120]}")
        if self.failures:
            context.partial = True
            context.warning_codes.extend(f"EVIDENCE_UNAVAILABLE:{failure}" for failure in self.failures[:4])
        return output


def persist_graph_context(control_plane: Any, *, result: Mapping[str, Any], context: Mapping[str, Any],
                         run_id: str, tenant_id: str, cluster_id: str) -> dict[str, Any]:
    """Persist a graph context through the query-api control-plane boundary."""
    return control_plane.append_graph_context(
        run_id=run_id, tenant_id=tenant_id, cluster_id=cluster_id,
        context_version=int(context.get("context_version") or 1), context=context,
        trigger_entity_uid=str(context.get("symptom_entity_uid") or ""),
        root_cause_entity_uid=str(result.get("root_cause") or ""),
        is_final=False, graph_generation=int(context.get("graph_generation") or 0),
        graph_schema_version=int(context.get("graph_schema_version") or 2),
    )
