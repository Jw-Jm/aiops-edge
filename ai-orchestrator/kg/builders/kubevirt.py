from __future__ import annotations
from typing import Any, Iterable
from ..identity import k8s_entity_uid, kubevirt_entity_uid
from .base import GraphBuilder

KIND_TO_TYPE = {
    "VirtualMachine": "vm", "VirtualMachineInstance": "vmi",
    "VirtualMachineInstanceMigration": "migration", "Pod": "pod", "Node": "k8s_node",
    "PersistentVolumeClaim": "pvc", "PVC": "pvc", "NetworkAttachmentDefinition": "nad", "NAD": "nad",
    "Network": "network",
}


class KubeVirtBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("kubevirt", tenant_id, cluster_id, generation, attrs_version)

    def build_entity(self, obj: dict[str, Any]):
        kind, metadata = str(obj.get("kind", "")), obj.get("metadata") or {}
        entity_type, object_uid = KIND_TO_TYPE.get(kind), str(metadata.get("uid") or "").strip()
        if not entity_type or not object_uid:
            return None
        uid = kubevirt_entity_uid(entity_type, self.cluster_id, object_uid) if entity_type in {"vm", "vmi", "migration"} \
            else k8s_entity_uid(entity_type, self.cluster_id, object_uid)
        return self.entity(uid=uid, entity_type=entity_type,
                           name=str(metadata.get("name") or object_uid), namespace=str(metadata.get("namespace") or ""),
                           source_uid=object_uid, attrs={"kind": kind, "resource_version": metadata.get("resourceVersion", "")})

    def build(self, objects: Iterable[dict[str, Any]]):
        objects = list(objects)
        entities = [e for e in (self.build_entity(o) for o in objects) if e is not None]
        by_uid = {e.source_uid: e for e in entities}
        by_name = {(e.entity_type, e.namespace, e.name): e for e in entities}
        mutations = [self.vertex_mutation(e) for e in entities]
        for obj in objects:
            child = by_uid.get(str((obj.get("metadata") or {}).get("uid") or ""))
            if not child:
                continue
            for owner in (obj.get("metadata") or {}).get("ownerReferences") or []:
                parent = by_uid.get(str(owner.get("uid") or ""))
                if parent and child.entity_type == "vmi" and parent.entity_type == "vm":
                    mutations.append(self.edge_mutation(self.edge(source=child, target=parent, relation_type="INSTANCE_OF")))
            if child.entity_type == "vmi":
                status = obj.get("status") or {}
                node_name = str(status.get("nodeName") or status.get("node") or "")
                node = by_name.get(("k8s_node", "", node_name))
                if node:
                    mutations.append(self.edge_mutation(self.edge(source=child, target=node, relation_type="RUNS_ON")))
            if child.entity_type == "pod":
                labels = (obj.get("metadata") or {}).get("labels") or {}
                vmi_name = str(labels.get("kubevirt.io/domain") or "")
                vmi = by_name.get(("vmi", child.namespace, vmi_name))
                if vmi:
                    mutations.append(self.edge_mutation(self.edge(source=child, target=vmi, relation_type="BELONGS_TO")))
            if child.entity_type == "vm":
                template = ((obj.get("spec") or {}).get("template") or {})
                template_spec = template.get("spec") or {}
                for volume in template_spec.get("volumes") or []:
                    claim_name = str((volume.get("persistentVolumeClaim") or {}).get("claimName") or "")
                    pvc = by_name.get(("pvc", child.namespace, claim_name))
                    if pvc:
                        mutations.append(self.edge_mutation(self.edge(source=child, target=pvc, relation_type="USES_VOLUME")))
                for network in template_spec.get("networks") or []:
                    network_name = str(network.get("name") or "")
                    target = by_name.get(("nad", child.namespace, network_name)) or by_name.get(("network", child.namespace, network_name))
                    if target:
                        mutations.append(self.edge_mutation(self.edge(source=child, target=target, relation_type="ATTACHED_TO")))
        return self.batch(mutations)
