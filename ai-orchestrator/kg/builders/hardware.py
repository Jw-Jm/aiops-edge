from __future__ import annotations
from typing import Any, Iterable
from ..identity import asset_uuid_v5, hardware_component_uid, physical_server_uid
from .base import GraphBuilder


class UnresolvedHardwareIdentity(ValueError):
    """A hostname alone is not a production hardware identity."""


class HardwareBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("hardware", tenant_id, "", generation, attrs_version)

    def build_server(self, record: dict[str, Any]):
        asset = asset_uuid_v5(self.tenant_id, str(record.get("system_uuid", "")),
                              str(record.get("vendor", "")), str(record.get("serial", "")))
        if not asset:
            raise UnresolvedHardwareIdentity("hardware record has no stable system_uuid or vendor+serial")
        return self.entity(uid=physical_server_uid(asset), entity_type="physical_server",
                           name=str(record.get("hostname") or record.get("serial") or asset), source_uid=asset,
                           attrs={"asset_uuid": asset, "vendor": record.get("vendor", ""), "serial": record.get("serial", "")})

    def build(self, records: Iterable[dict[str, Any]]):
        mutations = []
        for record in records:
            server = self.build_server(record)
            mutations.append(self.vertex_mutation(server))
            for component in record.get("components") or []:
                component_type = str(component.get("type") or "").strip().lower()
                locator = str(component.get("stable_locator") or component.get("serial") or "").strip()
                if not component_type or not locator:
                    continue
                item = self.entity(uid=hardware_component_uid(server.source_uid, component_type, locator),
                                   entity_type=component_type, name=str(component.get("name") or locator),
                                   source_uid=locator, attrs=dict(component))
                mutations.extend((self.vertex_mutation(item), self.edge_mutation(
                    self.edge(source=server, target=item, relation_type="HAS_COMPONENT"))))
        return self.batch(mutations)
