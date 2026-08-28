from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any


@dataclass
class GraphContext:
    run_id: str
    tenant_id: str
    primary_cluster_id: str
    contract_version: str = "graph-dto-v1"
    schema_version: int = 2
    graph_generation: int = 0
    context_version: int = 1
    partial: bool = False
    stale: bool = False
    warning_codes: list[str] = field(default_factory=list)
    vertices: list[dict[str, Any]] = field(default_factory=list)
    edges: list[dict[str, Any]] = field(default_factory=list)
    propagation_paths: list[dict[str, Any]] = field(default_factory=list)
    events: list[str] = field(default_factory=list)
    snapshot_at: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())
    window_start: str = ""
    window_end: str = ""
    symptom_time: str = ""
    symptom_entity_uid: str = ""

    def record(self, event: str) -> None:
        self.events.append(event)
        self.context_version += 1

    def to_dict(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id, "tenant_id": self.tenant_id, "primary_cluster_id": self.primary_cluster_id,
            "contract_version": self.contract_version, "graph_schema_version": self.schema_version,
            "graph_generation": self.graph_generation, "context_version": self.context_version,
            "snapshot_at": self.snapshot_at, "window_start": self.window_start, "window_end": self.window_end,
            "symptom_time": self.symptom_time, "symptom_entity_uid": self.symptom_entity_uid,
            "partial": self.partial, "stale": self.stale,
            "warning_codes": list(self.warning_codes), "vertices": list(self.vertices), "edges": list(self.edges),
            "propagation_paths": list(self.propagation_paths),
            "events": list(self.events),
        }
