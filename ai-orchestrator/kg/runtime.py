"""Canonical source reconciliation runtime for graph-backend v2.

This module is the only orchestrator-side scheduler for external graph facts.
It deliberately keeps source access behind :class:`InternalQueryClient` and
keeps graph writes behind :class:`ControlPlaneClient`; no database, Kubernetes
client, or HugeGraph client is imported here.

The lifecycle mirrors the production plan (section 19): acquire a per-source
scope, start a durable reconcile run, read a complete canonical response,
build deterministic mutations, project every batch, mark stale only after all
batches succeed, and finish the run.  Empty or partial facts are safe no-ops.
"""
from __future__ import annotations

import asyncio
import os
import uuid
from dataclasses import dataclass, replace
from typing import Any, Callable, Mapping

from .backfill import BACKFILL_ORDER
from .builders import (
    CatalogBuilder,
    ChangeBuilder,
    HardwareBuilder,
    KubeVirtBuilder,
    KubernetesBuilder,
    MiddlewareBuilder,
    NetworkBuilder,
    TraceBuilder,
)
from .models import GraphMutationBatch
from .scheduler import interval_seconds


MAX_BATCH_MUTATIONS = 500


@dataclass(frozen=True)
class SourceSpec:
    source: str
    operations: tuple[str, ...]
    tools: tuple[str, ...]
    interval_source: str
    builder: Callable[..., Any]


@dataclass(frozen=True)
class SourceRunResult:
    source: str
    status: str
    generation: int = 0
    mutations: int = 0
    batches: int = 0
    error: str = ""


SOURCE_SPECS: dict[str, SourceSpec] = {
    # Catalog is authoritative MySQL data.  Its normal mutation path is
    # MySQL transaction + outbox; the startup/full audit read is only for
    # importing an already-existing catalog into a fresh graph.
    "catalog": SourceSpec("catalog", ("catalog",), ("query_business_catalog.v1",), "audit", CatalogBuilder),
    "hardware": SourceSpec(
        "hardware", ("hardware_inventory", "hardware_health"),
        ("query_hardware_inventory.v1", "query_hardware_health.v1"), "hardware", HardwareBuilder,
    ),
    "kubernetes": SourceSpec("kubernetes", ("kubernetes",), ("query_k8s.v1",), "kubernetes", KubernetesBuilder),
    "kubevirt": SourceSpec("kubevirt", ("kubevirt",), ("query_kubevirt.v1",), "kubevirt", KubeVirtBuilder),
    "middleware": SourceSpec("middleware", ("middleware",), ("query_middleware.v1",), "middleware", MiddlewareBuilder),
    "trace": SourceSpec("trace", ("topology",), ("query_topology.v1",), "trace", TraceBuilder),
    "change": SourceSpec("change", ("changes",), ("query_changes.v1",), "audit", ChangeBuilder),
    "network": SourceSpec(
        "network", ("network_topology",), ("query_network_topology.v1",), "network", NetworkBuilder,
    ),
}


def _envelope_body(result: Any) -> tuple[str, Mapping[str, Any] | list[Any]]:
    """Return quality and canonical data from a QueryResult or test double."""
    body = getattr(result, "body", result)
    if not isinstance(body, Mapping):
        raise ValueError("canonical query returned a non-object envelope")
    quality = str(body.get("quality") or "complete").strip().lower()
    data = body.get("data", body)
    if isinstance(data, (bytes, bytearray)):
        raise ValueError("canonical query returned undecoded data")
    if not isinstance(data, (Mapping, list)):
        raise ValueError("canonical query returned invalid data")
    return quality, data


def _items(data: Mapping[str, Any] | list[Any], *keys: str) -> list[dict[str, Any]]:
    if isinstance(data, list):
        return [item for item in data if isinstance(item, Mapping)]
    values: list[dict[str, Any]] = []
    for key in keys:
        value = data.get(key)
        if isinstance(value, list):
            values.extend(item for item in value if isinstance(item, Mapping))
    if not values and isinstance(data.get("items"), list):
        values.extend(item for item in data["items"] if isinstance(item, Mapping))
    return values


def _k8s_objects(data: Mapping[str, Any] | list[Any]) -> list[dict[str, Any]]:
    if isinstance(data, list):
        return [dict(item) for item in data if isinstance(item, Mapping)]
    field_kinds = (
        ("cluster", "Cluster"), ("nodes", "Node"), ("namespaces", "Namespace"),
        ("deployments", "Deployment"), ("replicasets", "ReplicaSet"),
        ("statefulsets", "StatefulSet"), ("daemonsets", "DaemonSet"),
        ("pods", "Pod"), ("containers", "Container"), ("services", "Service"),
        ("endpoint_slices", "EndpointSlice"), ("pvcs", "PersistentVolumeClaim"),
        ("pvs", "PersistentVolume"), ("storage_classes", "StorageClass"),
        ("nads", "NetworkAttachmentDefinition"), ("networks", "Network"),
    )
    result: list[dict[str, Any]] = []
    for field, kind in field_kinds:
        value = data.get(field)
        if isinstance(value, Mapping):
            value = [value]
        if not isinstance(value, list):
            continue
        for raw in value:
            if not isinstance(raw, Mapping):
                continue
            item = dict(raw)
            item.setdefault("kind", kind)
            result.append(item)
    return result


def _catalog_records(data: Mapping[str, Any] | list[Any]) -> list[dict[str, Any]]:
    if isinstance(data, list):
        return [dict(item) for item in data if isinstance(item, Mapping)]
    result: list[dict[str, Any]] = []
    for field, entity_type, id_keys in (
        ("businesses", "business", ("business_uuid", "id")),
        ("applications", "application", ("application_uuid", "id")),
        ("services", "service", ("service_uuid", "id")),
        ("middleware", "middleware", ("middleware_uuid", "uid", "id")),
    ):
        for raw in _items(data, field):
            item = dict(raw)
            item["entity_type"] = entity_type
            item["id"] = next((str(item[key]) for key in id_keys if item.get(key)), "")
            if entity_type == "application":
                item.setdefault("business_id", item.get("business_uuid", ""))
            if entity_type == "service":
                item.setdefault("application_id", item.get("application_uuid", ""))
            result.append(item)
    if not result:
        result.extend(dict(item) for item in _items(data, "items", "records"))
    return result


def _source_records(source: str, data: Mapping[str, Any] | list[Any]) -> list[dict[str, Any]]:
    if source == "kubernetes":
        return _k8s_objects(data)
    if source == "kubevirt":
        if isinstance(data, list):
            return [dict(item) for item in data if isinstance(item, Mapping)]
        result: list[dict[str, Any]] = []
        for field, kind in (
            ("virtual_machines", "VirtualMachine"),
            ("virtual_machine_instances", "VirtualMachineInstance"),
            ("migrations", "VirtualMachineInstanceMigration"),
            ("virt_launcher_pods", "Pod"), ("nodes", "Node"),
            ("pvcs", "PersistentVolumeClaim"), ("nads", "NetworkAttachmentDefinition"),
            ("networks", "Network"),
        ):
            for raw in _items(data, field):
                item = dict(raw)
                item.setdefault("kind", kind)
                result.append(item)
        return result
    if source == "catalog":
        return _catalog_records(data)
    if source == "hardware":
        if isinstance(data, list):
            return [dict(item) for item in data if isinstance(item, Mapping)]
        return [dict(item) for item in _items(data, "servers", "assets", "items", "records")]
    if source == "middleware":
        result = []
        for raw in _items(data, "dependencies", "middleware", "items", "records"):
            item = dict(raw)
            database = str(item.get("name") or item.get("db_system") or "").strip()
            if database:
                item["name"] = database
                item.setdefault("uid", f"logical:{database}")
                item.setdefault("source_service", item.get("service_name") or item.get("service") or "")
                result.append(item)
        return result
    if source == "trace":
        result = []
        for raw in _items(data, "dependencies", "edges", "items", "records"):
            item = dict(raw)
            item.setdefault("source_service", item.get("source", ""))
            item.setdefault("target_service", item.get("target", ""))
            result.append(item)
        return result
    if source == "change":
        return [dict(item) for item in _items(data, "changes", "items", "records")]
    return [dict(item) for item in _items(data, "items", "records")]


def _builder_instance(spec: SourceSpec, tenant_id: str, cluster_id: str, generation: int,
                      overrides: Mapping[str, Callable[..., Any]]) -> Any:
    builder_type = overrides.get(spec.source, spec.builder)
    if spec.source in {"catalog", "hardware"}:
        return builder_type(tenant_id, generation=generation)
    return builder_type(tenant_id, cluster_id, generation=generation)


class GraphSyncRuntime:
    """One process-local scheduler backed by query-api durable leases."""

    def __init__(self, *, query_client: Any, control_plane: Any, tenant_id: str, cluster_id: str,
                 builders: Mapping[str, Callable[..., Any]] | None = None,
                 context_ref_factory: Callable[[str], str] | None = None) -> None:
        if not tenant_id or not cluster_id:
            raise ValueError("graph sync runtime requires tenant_id and cluster_id")
        self.query_client = query_client
        self.control_plane = control_plane
        self.tenant_id = tenant_id
        self.cluster_id = cluster_id
        self.builders = dict(builders or {})
        self.context_ref_factory = context_ref_factory or (
            lambda source: f"graph-reconcile:{source}:{uuid.uuid4()}"
        )
        self._task: asyncio.Task | None = None
        self._stop = asyncio.Event()

    def _query(self, tool_id: str, operation: str) -> tuple[str, Mapping[str, Any] | list[Any]]:
        params = {"namespace": "all"} if operation == "kubernetes" else {}
        result = self.query_client.query(
            tool_id=tool_id, operation=operation, tenant_id=self.tenant_id,
            cluster_id=self.cluster_id, params=params,
            context_ref=self.context_ref_factory(operation),
        )
        return _envelope_body(result)

    def _fetch_source(self, spec: SourceSpec) -> tuple[list[dict[str, Any]], str]:
        quality, data = self._query(spec.tools[0], spec.operations[0])
        if quality != "complete":
            raise ValueError(f"canonical {spec.operations[0]} quality={quality}")
        if spec.source != "hardware":
            return _source_records(spec.source, data), ""

        # Hardware health is a supplementary canonical fact.  It must be
        # complete before inventory mutations are considered complete, but an
        # explicitly empty health view is valid and does not erase inventory.
        health_quality, health_data = self._query(spec.tools[1], spec.operations[1])
        if health_quality != "complete":
            raise ValueError(f"canonical {spec.operations[1]} quality={health_quality}")
        inventory = _source_records(spec.source, data)
        health = _items(health_data, "items", "health", "records")
        by_key = {
            str(item.get("system_uuid") or item.get("asset_uuid") or item.get("serial") or ""): item
            for item in inventory
        }
        for item in health:
            key = str(item.get("system_uuid") or item.get("asset_uuid") or item.get("serial") or "")
            if key and key in by_key:
                by_key[key].setdefault("health", []).append(dict(item))
        return inventory, ""

    def _control(self, operation: str, body: Mapping[str, Any], *, write: bool = True) -> dict:
        result = self.control_plane.knowledge_graph(operation, dict(body), write=write, tenant_id=self.tenant_id)
        return result if isinstance(result, Mapping) else {}

    def _finish(self, *, run_id: str, source: str, generation: int, status: str,
                watermark: str = "", error: str = "", vertices_seen: int = 0,
                edges_seen: int = 0, vertices_staled: int = 0, edges_staled: int = 0,
                lease: Mapping[str, Any] | None = None) -> None:
        self._control("reconcile_scope", {
            "phase": status, "reconcile_run_id": run_id, "source": source,
            "cluster_id": self.cluster_id, "generation": generation,
            "watermark": watermark, "error": error,
            "vertices_seen": vertices_seen, "edges_seen": edges_seen,
            "vertices_staled": vertices_staled, "edges_staled": edges_staled,
            **dict(lease or {}),
        })

    def run_once(self, source: str) -> SourceRunResult:
        spec = SOURCE_SPECS.get(source)
        if spec is None:
            return SourceRunResult(source, "failed", error="unknown graph source")

        run_id = ""
        generation = 0
        vertices_seen = 0
        edges_seen = 0
        lease: dict[str, Any] = {}
        try:
            started = self._control("reconcile_scope", {
                "phase": "start", "source": source, "cluster_id": self.cluster_id,
            })
            if not started.get("acquired", False):
                return SourceRunResult(source, "skipped", error="scope lease is held by another worker")
            run_id = str(started.get("reconcile_run_id") or "")
            generation = int(started.get("generation") or 0)
            lease = {
                "lease_key": str(started.get("lease_key") or ""),
                "lease_owner_id": str(started.get("lease_owner_id") or ""),
                "lease_epoch": int(started.get("lease_epoch") or 0),
                "lease_token": str(started.get("lease_token") or ""),
            }
            if not run_id or generation <= 0:
                raise ValueError("reconcile start returned no durable run identity")

            records, watermark = self._fetch_source(spec)
            builder = _builder_instance(spec, self.tenant_id, self.cluster_id, generation, self.builders)
            batch = builder.build(records)
            if not isinstance(batch, GraphMutationBatch):
                raise ValueError("graph builder returned invalid mutation batch")
            if not batch.mutations:
                self._finish(run_id=run_id, source=source, generation=generation, status="no_data",
                             watermark=watermark, lease=lease)
                return SourceRunResult(source, "no_data", generation=generation)

            vertices_seen = len(batch.vertices)
            edges_seen = len(batch.edges)
            batches = 0
            for start in range(0, len(batch.mutations), MAX_BATCH_MUTATIONS):
                chunk = replace(batch, mutations=batch.mutations[start:start + MAX_BATCH_MUTATIONS])
                self._control("batch_mutate", {**chunk.to_dict(), **lease})
                batches += 1
            stale = self._control("mark_stale_generation", {
                "source": source, "cluster_id": self.cluster_id, "generation": generation,
                **lease,
            })
            self._finish(run_id=run_id, source=source, generation=generation, status="success",
                         watermark=watermark, vertices_seen=vertices_seen, edges_seen=edges_seen,
                         vertices_staled=int(stale.get("marked_vertices") or 0),
                         edges_staled=int(stale.get("marked_edges") or 0), lease=lease)
            return SourceRunResult(source, "success", generation, len(batch.mutations), batches)
        except Exception as exc:  # noqa: BLE001 - one source must not stop other source cycles
            message = str(exc)[:500]
            if run_id:
                try:
                    self._finish(run_id=run_id, source=source, generation=generation,
                                 status="failed", error=message, vertices_seen=vertices_seen,
                                 edges_seen=edges_seen, lease=lease)
                except Exception:
                    pass
            return SourceRunResult(source, "failed", generation=generation, error=message)

    def run_all_once(self) -> list[SourceRunResult]:
        """Run the documented backfill order; failures remain source-local."""
        return [self.run_once(source) for source in BACKFILL_ORDER]

    async def _run_loop(self) -> None:
        initial_results = await asyncio.to_thread(self.run_all_once)
        for result in initial_results:
            print(f"[kg-reconcile] source={result.source} status={result.status} "
                  f"generation={result.generation} mutations={result.mutations} "
                  f"batches={result.batches} error={result.error[:200]}", flush=True)
        due = {source: asyncio.get_running_loop().time() + interval_seconds(SOURCE_SPECS[source].interval_source)
               for source in SOURCE_SPECS}
        while not self._stop.is_set():
            now = asyncio.get_running_loop().time()
            source = min(due, key=due.get)
            wait = max(0.1, due[source] - now)
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=wait)
            except asyncio.TimeoutError:
                result = await asyncio.to_thread(self.run_once, source)
                print(f"[kg-reconcile] source={result.source} status={result.status} "
                      f"generation={result.generation} mutations={result.mutations} "
                      f"batches={result.batches} error={result.error[:200]}", flush=True)
                due[source] = asyncio.get_running_loop().time() + interval_seconds(SOURCE_SPECS[source].interval_source)

    def start(self) -> asyncio.Task:
        if self._task is None or self._task.done():
            self._stop.clear()
            self._task = asyncio.create_task(self._run_loop(), name="graph-source-reconcile")
        return self._task

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            self._task.cancel()
            await asyncio.gather(self._task, return_exceptions=True)
            self._task = None


def build_graph_sync_runtime() -> GraphSyncRuntime:
    """Construct the production runtime from the existing trusted clients."""
    from control_plane_client import ControlPlaneClient
    from internal_query import _load_private_key
    from internal_query_client import InternalQueryClient
    from trusted_context_issuer import TrustedContextIssuer
    from tool_registry import init_default_tool_registry

    tenant_id = os.environ.get("AIOPS_SYSTEM_TENANT_ID", "").strip()
    cluster_id = os.environ.get("AIOPS_SYSTEM_CLUSTER_ID", "").strip()
    if not tenant_id or not cluster_id:
        raise RuntimeError("AIOPS_SYSTEM_TENANT_ID and AIOPS_SYSTEM_CLUSTER_ID are required")
    init_default_tool_registry()
    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    return GraphSyncRuntime(
        query_client=InternalQueryClient(issuer=TrustedContextIssuer(private_key=private_key)),
        control_plane=ControlPlaneClient(), tenant_id=tenant_id, cluster_id=cluster_id,
    )
