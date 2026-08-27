from __future__ import annotations
from typing import Any, Iterable
from ..identity import entity_uid
from .base import GraphBuilder


class NetworkBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("network", tenant_id, cluster_id, generation, attrs_version)

    def build(self, records: Iterable[dict[str, Any]]):
        by_uid, mutations = {}, []
        for record in records:
            typ, stable_id = str(record.get("entity_type") or "network"), str(record.get("uid") or "").strip()
            if typ not in {"nad", "network", "switch", "switch_port", "nic"} or not stable_id:
                continue
            item = self.entity(uid=entity_uid(typ, self.tenant_id, self.cluster_id, stable_id), entity_type=typ,
                               name=str(record.get("name") or stable_id), source_uid=stable_id, attrs=dict(record))
            by_uid[stable_id] = item; mutations.append(self.vertex_mutation(item))
        for record in records:
            src, dst = by_uid.get(str(record.get("source_uid") or "")), by_uid.get(str(record.get("target_uid") or ""))
            if src and dst:
                mutations.append(self.edge_mutation(self.edge(source=src, target=dst, relation_type="CONNECTS_TO")))
        return self.batch(mutations)
