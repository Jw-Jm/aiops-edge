from __future__ import annotations
from typing import Any, Iterable
from ..identity import canonical_service_uid, entity_uid
from .base import GraphBuilder


class CatalogBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("catalog", tenant_id, "", generation, attrs_version)

    def build(self, records: Iterable[dict[str, Any]]):
        records = list(records)
        entities, by_id, mutations = [], {}, []
        for record in records:
            typ, stable_id = str(record.get("entity_type") or "service"), str(record.get("id") or "").strip()
            if not stable_id:
                continue
            uid = canonical_service_uid(self.tenant_id, stable_id) if typ == "service" else entity_uid(typ, self.tenant_id, stable_id)
            item = self.entity(uid=uid, entity_type=typ, name=str(record.get("name") or stable_id), source_uid=stable_id,
                               attrs={k: v for k, v in record.items() if k not in {"id", "name", "entity_type"}})
            entities.append(item); by_id[stable_id] = item; mutations.append(self.vertex_mutation(item))
        for record in records:
            source, target = by_id.get(str(record.get("id") or "")), by_id.get(str(record.get("application_id") or ""))
            if source and target and source.entity_type == "service" and target.entity_type == "application":
                mutations.append(self.edge_mutation(self.edge(source=source, target=target, relation_type="BELONGS_TO")))
            business = by_id.get(str(record.get("business_id") or record.get("business_uuid") or ""))
            if source and business and source.entity_type == "application" and business.entity_type == "business":
                mutations.append(self.edge_mutation(self.edge(source=source, target=business, relation_type="BELONGS_TO")))
        return self.batch(mutations)
