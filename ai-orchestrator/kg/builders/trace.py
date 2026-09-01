from __future__ import annotations
from typing import Any, Iterable
from ..identity import provisional_service_uid
from .base import GraphBuilder


class TraceBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("trace", tenant_id, cluster_id, generation, attrs_version)

    def build(self, dependencies: Iterable[dict[str, Any]]):
        services, mutations = {}, []
        for row in dependencies:
            for field in ("source_service", "target_service"):
                name = str(row.get(field) or "").strip()
                if name and name not in services:
                    services[name] = self.entity(uid=provisional_service_uid(self.tenant_id, self.cluster_id, name),
                                                 entity_type="service", name=name, confidence=0.90,
                                                 resolution="provisional", attrs={"observed_by": "trace"})
                    mutations.append(self.vertex_mutation(services[name]))
            source, target = services.get(str(row.get("source_service") or "")), services.get(str(row.get("target_service") or ""))
            # Self-observations (for example exporter/ingest loops) are not
            # dependency edges.  Persisting them makes HugeGraph traversals
            # return a misleading single-node graph and prevents RCA from
            # seeing the real cross-service propagation path.
            if source and target and source.entity_uid != target.entity_uid:
                mutations.append(self.edge_mutation(self.edge(source=source, target=target, relation_type="DEPENDS_ON",
                                                              confidence=min(float(row.get("confidence", 0.90)), 0.90))))
        return self.batch(mutations)
