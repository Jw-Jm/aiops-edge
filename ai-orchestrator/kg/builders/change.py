from __future__ import annotations
from typing import Any, Iterable
from ..identity import entity_uid
from .base import GraphBuilder


class ChangeBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("change", tenant_id, cluster_id, generation, attrs_version)

    def build(self, records: Iterable[dict[str, Any]]):
        mutations = []
        for record in records:
            stable_id = str(record.get("change_id") or record.get("uid") or "").strip()
            if not stable_id:
                continue
            change = self.entity(uid=entity_uid("change", self.tenant_id, self.cluster_id, stable_id),
                                 entity_type="change", name=str(record.get("summary") or stable_id), source_uid=stable_id,
                                 attrs=dict(record))
            mutations.append(self.vertex_mutation(change))
        return self.batch(mutations)
