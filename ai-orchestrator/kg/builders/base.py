from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable

from ..identity import name_key_v1
from ..models import Edge, Entity, GraphMutation, GraphMutationBatch


@dataclass
class GraphBuilder:
    source: str
    tenant_id: str
    cluster_id: str
    generation: int = 1
    attrs_version: int = 1

    def entity(self, *, uid: str, entity_type: str, name: str, source_uid: str = "", namespace: str = "",
               confidence: float = 1.0, attrs: dict[str, Any] | None = None, **kwargs: Any) -> Entity:
        return Entity(entity_uid=uid, entity_type=entity_type, tenant_id=self.tenant_id,
                      cluster_id=self.cluster_id, name=name, name_key=name_key_v1(name), source=self.source,
                      source_uid=source_uid, namespace=namespace, confidence=confidence,
                      generation=self.generation, attrs_version=self.attrs_version, attrs=attrs or {}, **kwargs)

    def batch(self, mutations: Iterable[GraphMutation]) -> GraphMutationBatch:
        return GraphMutationBatch(self.tenant_id, self.cluster_id, self.source, self.generation,
                                  self.attrs_version, tuple(mutations))

    def vertex_mutation(self, entity: Entity) -> GraphMutation:
        return GraphMutation.upsert_vertex(entity, source=self.source, generation=self.generation)

    def edge_mutation(self, edge: Edge) -> GraphMutation:
        return GraphMutation.upsert_edge(edge, source=self.source, generation=self.generation)

    def edge(self, *, source: Entity, target: Entity, relation_type: str, confidence: float = 1.0,
             attrs: dict[str, Any] | None = None, **kwargs: Any) -> Edge:
        return Edge.create(source_uid=source.entity_uid, target_uid=target.entity_uid,
                           relation_type=relation_type, source=self.source, tenant_id=self.tenant_id,
                           cluster_id=self.cluster_id, source_type=source.entity_type,
                           target_type=target.entity_type, confidence=confidence,
                           generation=self.generation, attrs_version=self.attrs_version,
                           attrs=attrs or {}, **kwargs)
