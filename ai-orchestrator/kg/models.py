"""Typed, JSON-friendly graph DTOs and deterministic mutations."""
from __future__ import annotations

from dataclasses import dataclass, field, asdict
from typing import Any, Mapping

from .identity import edge_uid as make_edge_uid, mutation_uuid
from .ontology import validate_entity_type, validate_relation


@dataclass(frozen=True)
class Entity:
    entity_uid: str
    entity_type: str
    tenant_id: str
    cluster_id: str
    name: str
    source: str
    name_key: str = ""
    namespace: str = ""
    source_uid: str = ""
    status: str = "active"
    health: str = ""
    resolution: str = ""
    confidence: float = 1.0
    first_seen_ms: int = 0
    last_seen_ms: int = 0
    generation: int = 0
    attrs_version: int = 1
    attrs: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        validate_entity_type(self.entity_type)

    def to_dict(self) -> dict[str, Any]:
        value = asdict(self)
        if not value["name_key"]:
            from .identity import name_key_v1
            value["name_key"] = name_key_v1(self.name)
        return value


@dataclass(frozen=True)
class Edge:
    edge_uid: str
    source_uid: str
    target_uid: str
    relation_type: str
    tenant_id: str
    cluster_id: str
    source: str
    confidence: float = 1.0
    status: str = "active"
    generation: int = 0
    first_seen_ms: int = 0
    last_seen_ms: int = 0
    valid_from_ms: int = 0
    valid_to_ms: int = 0
    propagates_failure: bool = True
    candidate_direction: str = "BOTH"
    impact_direction: str = "BOTH"
    attrs_version: int = 1
    attrs: Mapping[str, Any] = field(default_factory=dict)

    @classmethod
    def create(cls, *, source_uid: str, target_uid: str, relation_type: str, source: str,
               tenant_id: str, cluster_id: str, source_type: str, target_type: str, **kwargs: Any) -> "Edge":
        relation_type = validate_relation(relation_type, source_type, target_type)
        return cls(edge_uid=make_edge_uid(tenant_id, relation_type, source_uid, target_uid),
                    source_uid=source_uid, target_uid=target_uid, relation_type=relation_type,
                    tenant_id=tenant_id, cluster_id=cluster_id, source=source, **kwargs)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class GraphMutation:
    mutation_id: str
    kind: str
    vertex: dict[str, Any] | None
    edge: dict[str, Any] | None
    source: str
    generation: int

    @classmethod
    def upsert_vertex(cls, vertex: Entity, *, source: str, generation: int) -> "GraphMutation":
        return cls(mutation_uuid("upsert_vertex", vertex.entity_uid, vertex.attrs_version, generation),
                   "upsert_vertex", vertex.to_dict(), None, source, generation)

    @classmethod
    def upsert_edge(cls, edge: Edge, *, source: str, generation: int) -> "GraphMutation":
        return cls(mutation_uuid("upsert_edge", edge.edge_uid, edge.attrs_version, generation),
                   "upsert_edge", None, edge.to_dict(), source, generation)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class GraphMutationBatch:
    tenant_id: str
    cluster_id: str
    source: str
    generation: int
    attrs_version: int
    mutations: tuple[GraphMutation, ...] = ()

    @property
    def vertices(self) -> list[dict[str, Any]]:
        return [m.vertex for m in self.mutations if m.vertex is not None]

    @property
    def edges(self) -> list[dict[str, Any]]:
        return [m.edge for m in self.mutations if m.edge is not None]

    def to_dict(self) -> dict[str, Any]:
        return {"tenant_id": self.tenant_id, "cluster_id": self.cluster_id, "source": self.source,
                "generation": self.generation, "attrs_version": self.attrs_version,
                "mutations": [m.to_dict() for m in self.mutations],
                "vertices": self.vertices, "edges": self.edges}
