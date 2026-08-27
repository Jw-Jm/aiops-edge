from __future__ import annotations

from typing import Any, Iterable

from ..identity import k8s_entity_uid, sha256_parts
from .base import GraphBuilder

KIND_TO_TYPE = {
    "Cluster": "k8s_cluster", "Namespace": "namespace", "Node": "k8s_node", "Deployment": "deployment",
    "ReplicaSet": "replicaset", "StatefulSet": "statefulset", "DaemonSet": "daemonset", "Pod": "pod",
    "Container": "container", "Service": "k8s_service", "EndpointSlice": "endpoint_slice", "PVC": "pvc",
    "PersistentVolumeClaim": "pvc", "PV": "pv", "PersistentVolume": "pv", "StorageClass": "storage_class",
    "NAD": "nad", "NetworkAttachmentDefinition": "nad", "Network": "network",
}


class KubernetesBuilder(GraphBuilder):
    def __init__(self, tenant_id: str, cluster_id: str, generation: int = 1, attrs_version: int = 1):
        super().__init__("kubernetes", tenant_id, cluster_id, generation, attrs_version)

    def build_entity(self, obj: dict[str, Any]):
        kind = str(obj.get("kind", ""))
        entity_type = KIND_TO_TYPE.get(kind)
        metadata = obj.get("metadata") or {}
        if kind == "Container":
            pod_uid = str(obj.get("pod_uid") or metadata.get("pod_uid") or "").strip()
            container_name = str(obj.get("name") or metadata.get("name") or "").strip()
            if not pod_uid or not container_name:
                return None
            object_uid = f"{pod_uid}:{sha256_parts(container_name)}"
        else:
            object_uid = str(metadata.get("uid") or "").strip()
        if not entity_type or not object_uid:
            return None
        name = str(obj.get("name") or metadata.get("name") or object_uid)
        entity = self.entity(uid=k8s_entity_uid(entity_type, self.cluster_id, object_uid),
                              entity_type=entity_type, name=name,
                              source_uid=object_uid, namespace=str(metadata.get("namespace") or ""),
                              attrs={"kind": kind, "resource_version": metadata.get("resourceVersion", ""),
                                     **({"pod_uid": str(obj.get("pod_uid") or metadata.get("pod_uid") or ""),
                                         "container_name": name} if kind == "Container" else {})})
        return entity

    def build(self, objects: Iterable[dict[str, Any]]) -> Any:
        objects = list(objects)
        entities = [e for e in (self.build_entity(o) for o in objects) if e is not None]
        # The Kubernetes API does not assign UIDs to individual containers.
        # Materialize their deterministic identities from the Pod UID and
        # container name as required by graph-identity-v1.
        for obj in objects:
            if str(obj.get("kind") or "") != "Pod":
                continue
            pod_uid = str((obj.get("metadata") or {}).get("uid") or "").strip()
            if not pod_uid:
                continue
            for container in (obj.get("spec") or {}).get("containers") or []:
                if not isinstance(container, dict) or not container.get("name"):
                    continue
                entities.append(self.build_entity({
                    "kind": "Container", "pod_uid": pod_uid,
                    "name": container["name"], "metadata": {"namespace": (obj.get("metadata") or {}).get("namespace", "")},
                }))
        entities = [entity for entity in entities if entity is not None]
        by_source_uid = {e.source_uid: e for e in entities}
        by_name = {(e.entity_type, e.namespace, e.name): e for e in entities}
        mutations = [self.vertex_mutation(e) for e in entities]
        edge_seen: set[tuple[str, str, str]] = set()

        def add_edge(source, target, relation_type: str, *, confidence: float = 1.0, attrs: dict[str, Any] | None = None):
            key = (source.entity_uid, target.entity_uid, relation_type)
            if key in edge_seen:
                return
            edge_seen.add(key)
            mutations.append(self.edge_mutation(self.edge(source=source, target=target,
                                                          relation_type=relation_type,
                                                          confidence=confidence, attrs=attrs)))

        # The boundary supplies one synthetic Cluster object whose UID is the
        # authoritative kube-system identity. Namespace/node containment is
        # therefore deterministic even though Kubernetes objects have no owner
        # reference back to a Cluster.
        cluster = next((entity for entity in entities if entity.entity_type == "k8s_cluster"), None)
        if cluster:
            for entity in entities:
                if entity.entity_type in {"namespace", "k8s_node"}:
                    add_edge(cluster, entity, "CONTAINS")

        pods_by_ip: dict[tuple[str, str], Any] = {}
        for obj in objects:
            if str(obj.get("kind") or "") != "Pod":
                continue
            metadata = obj.get("metadata") or {}
            pod = by_source_uid.get(str(metadata.get("uid") or ""))
            if pod is None:
                continue
            status = obj.get("status") or {}
            addresses = [status.get("podIP")]
            addresses.extend(item.get("ip") for item in (status.get("podIPs") or []) if isinstance(item, dict))
            for address in addresses:
                if address:
                    pods_by_ip[(pod.namespace, str(address))] = pod

        def endpoint_pods(endpoint_obj: dict[str, Any]) -> list[Any]:
            resolved = []
            seen = set()
            for endpoint in endpoint_obj.get("endpoints") or []:
                target = endpoint.get("targetRef") or {}
                pod = by_source_uid.get(str(target.get("uid") or ""))
                if not pod or pod.entity_type != "pod":
                    for address in endpoint.get("addresses") or []:
                        pod = pods_by_ip.get((str((endpoint_obj.get("metadata") or {}).get("namespace") or ""),
                                              str(address)))
                        if pod:
                            break
                if pod and pod.entity_type == "pod" and pod.entity_uid not in seen:
                    seen.add(pod.entity_uid)
                    resolved.append(pod)
            return resolved

        endpoint_backends = {
            str((endpoint.get("metadata") or {}).get("uid") or ""): endpoint_pods(endpoint)
            for endpoint in objects
            if str(endpoint.get("kind") or "") == "EndpointSlice"
        }

        def selector_matches(service_obj: dict[str, Any], pod: Any) -> bool:
            selector = ((service_obj.get("spec") or {}).get("selector") or {})
            if not selector:
                return False
            pod_obj = next((item for item in objects
                            if str(item.get("kind") or "") == "Pod"
                            and str((item.get("metadata") or {}).get("uid") or "") == pod.source_uid), None)
            labels = ((pod_obj or {}).get("metadata") or {}).get("labels") or {}
            return all(str(labels.get(str(key), "")) == str(value) for key, value in selector.items())

        for obj in objects:
            source = str((obj.get("metadata") or {}).get("uid") or "")
            child = by_source_uid.get(source)
            if child is None:
                continue
            for owner in (obj.get("metadata") or {}).get("ownerReferences") or []:
                parent = by_source_uid.get(str(owner.get("uid") or ""))
                if parent is None:
                    continue
                relation = "OWNS" if (parent.entity_type, child.entity_type) in {
                    ("deployment", "replicaset"), ("replicaset", "pod"), ("statefulset", "pod"), ("daemonset", "pod")
                } else "CONTAINS" if (parent.entity_type, child.entity_type) in {
                    ("k8s_cluster", "namespace"), ("k8s_cluster", "k8s_node")
                } else ""
                if relation:
                    add_edge(parent, child, relation)
            spec = obj.get("spec") or {}
            node_name = spec.get("nodeName")
            if child.entity_type == "pod" and node_name:
                node = by_name.get(("k8s_node", "", str(node_name)))
                if node:
                    add_edge(child, node, "RUNS_ON")
            if child.entity_type == "k8s_service":
                for endpoint in objects:
                    if str(endpoint.get("kind") or "") != "EndpointSlice":
                        continue
                    ep_meta = endpoint.get("metadata") or {}
                    ep_labels = ep_meta.get("labels") or {}
                    if str(ep_meta.get("namespace") or "") != child.namespace:
                        continue
                    ep_entity = by_source_uid.get(str(ep_meta.get("uid") or ""))
                    if not ep_entity:
                        continue
                    service_name = str(ep_labels.get("kubernetes.io/service-name") or "")
                    owner_uids = {str(owner.get("uid") or "") for owner in (ep_meta.get("ownerReferences") or [])}
                    endpoint_matches_selector = any(selector_matches(obj, pod)
                                                    for pod in endpoint_backends.get(ep_entity.source_uid, []))
                    # EndpointSlice's standard service-name label/owner UID is
                    # the authoritative association. A Service selector plus
                    # Pod labels is the documented lower-confidence fallback;
                    # no pod-name trimming or other name heuristic is used.
                    if service_name == child.name or child.source_uid in owner_uids:
                        add_edge(child, ep_entity, "TARGETS")
                    elif endpoint_matches_selector:
                        add_edge(child, ep_entity, "TARGETS", confidence=0.95,
                                 attrs={"resolution": "service_selector"})
            if child.entity_type == "endpoint_slice":
                for pod in endpoint_backends.get(child.source_uid, []):
                    add_edge(child, pod, "BACKED_BY")
            if child.entity_type == "pod":
                for volume in spec.get("volumes") or []:
                    claim = (volume.get("persistentVolumeClaim") or {}).get("claimName")
                    pvc = by_name.get(("pvc", child.namespace, str(claim or "")))
                    if pvc:
                        add_edge(child, pvc, "USES_VOLUME")
                for network in spec.get("networks") or []:
                    network_name = str(network.get("name") or "")
                    target = by_name.get(("nad", child.namespace, network_name)) or by_name.get(("network", child.namespace, network_name))
                    if target:
                        add_edge(child, target, "ATTACHED_TO")
            if child.entity_type == "pvc":
                pv_name = str(spec.get("volumeName") or "")
                pv = by_name.get(("pv", "", pv_name))
                if pv:
                    add_edge(child, pv, "BOUND_TO")
        return self.batch(mutations)
