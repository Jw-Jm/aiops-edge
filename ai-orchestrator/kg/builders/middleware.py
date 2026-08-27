from __future__ import annotations
from typing import Any, Iterable
from ..identity import entity_uid, provisional_service_uid
from .base import GraphBuilder


class MiddlewareBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("middleware", tenant_id, cluster_id, generation, attrs_version)

    def build(self, records: Iterable[dict[str, Any]]):
        records = list(records)
        by_name, services, mutations = {}, {}, []
        for record in records:
            name, stable_id = str(record.get("name") or "").strip(), str(record.get("uid") or "").strip()
            if not name or not stable_id:
                continue
            item = self.entity(uid=entity_uid("middleware", self.tenant_id, self.cluster_id, stable_id),
                               entity_type="middleware", name=name, source_uid=stable_id, attrs=dict(record))
            by_name[name] = item; mutations.append(self.vertex_mutation(item))
            service_name = str(record.get("source_service") or record.get("service_name") or record.get("service") or "").strip()
            if service_name and service_name not in services:
                service = self.entity(uid=provisional_service_uid(self.tenant_id, self.cluster_id, service_name),
                                      entity_type="service", name=service_name, confidence=0.90,
                                      resolution="provisional", attrs={"observed_by": "middleware"})
                services[service_name] = service
                mutations.append(self.vertex_mutation(service))
            if service_name and service_name in services:
                mutations.append(self.edge_mutation(self.edge(source=services[service_name], target=item,
                                                               relation_type="DEPENDS_ON")))
        for record in records:
            src, dst = by_name.get(str(record.get("source") or "")), by_name.get(str(record.get("target") or ""))
            if src and dst:
                mutations.append(self.edge_mutation(self.edge(source=src, target=dst, relation_type="DEPENDS_ON",
                                                              confidence=float(record.get("confidence", 1.0)))))
        return self.batch(mutations)
