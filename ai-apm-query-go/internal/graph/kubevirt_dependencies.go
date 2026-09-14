package graph

import (
	"errors"
	"sort"
	"strings"
)

// KubeVirtDependencyGraph is the typed dependency projection consumed by the
// resource detail and graph views. It deliberately keeps PVC/PV as Kubernetes
// storage identities; a VM disk is only a dependency edge, never a duplicate
// resource identity.
type KubeVirtDependencyGraph struct {
	Entities     []Entity `json:"entities"`
	Edges        []Edge   `json:"edges"`
	Partial      bool     `json:"partial"`
	WarningCodes []string `json:"warning_codes,omitempty"`
}

type kubeObjectRef struct {
	UID       string
	Name      string
	Namespace string
	Raw       map[string]interface{}
}

func snapshotObjects(snapshot map[string]interface{}, key string) []map[string]interface{} {
	value, ok := snapshot[key]
	if !ok {
		return nil
	}
	items, ok := value.([]map[string]interface{})
	if ok {
		return items
	}
	generic, ok := value.([]interface{})
	if !ok {
		return nil
	}
	items = make([]map[string]interface{}, 0, len(generic))
	for _, item := range generic {
		if object, ok := item.(map[string]interface{}); ok {
			items = append(items, object)
		}
	}
	return items
}

func objectRef(raw map[string]interface{}) kubeObjectRef {
	metadata, _ := raw["metadata"].(map[string]interface{})
	return kubeObjectRef{UID: kubeStringValue(metadata["uid"]), Name: kubeStringValue(metadata["name"]), Namespace: kubeStringValue(metadata["namespace"]), Raw: raw}
}

func kubeStringValue(value interface{}) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func kubeMapValue(value interface{}) map[string]interface{} {
	result, _ := value.(map[string]interface{})
	return result
}

func kubeListValue(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func lookupObject(index map[string]kubeObjectRef, namespace, name string) (kubeObjectRef, bool) {
	key := strings.Trim(strings.TrimSpace(namespace)+"/"+strings.TrimSpace(name), "/")
	item, ok := index[key]
	return item, ok
}

func indexObjects(snapshot map[string]interface{}, key string) map[string]kubeObjectRef {
	index := map[string]kubeObjectRef{}
	for _, raw := range snapshotObjects(snapshot, key) {
		item := objectRef(raw)
		if item.Name != "" {
			index[strings.Trim(item.Namespace+"/"+item.Name, "/")] = item
		}
	}
	return index
}

func dependencyEntity(entityType, uid, clusterID, tenantID, namespace, name string, attrs map[string]interface{}) Entity {
	entityUID := uid
	if uid == "" {
		uid = SHA256Parts(entityType, clusterID, namespace, name)
		entityUID = EntityUID("kubevirt-"+entityType, clusterID, uid)
	}
	return Entity{EntityUID: entityUID, EntityType: entityType, TenantID: tenantID, ClusterID: clusterID, Namespace: namespace, Name: name, NameKey: NameKeyV1(name), Source: "k8s-boundary", SourceUID: uid, Status: "active", Confidence: 1, Generation: 1, AttrsVersion: 1, Attrs: attrs}
}

func objectEntity(entityType, clusterID, tenantID string, object kubeObjectRef) Entity {
	uid := object.UID
	if uid == "" {
		uid = SHA256Parts(entityType, object.Namespace, object.Name)
	}
	return Entity{EntityUID: K8sEntityUID(entityType, clusterID, uid), EntityType: entityType, TenantID: tenantID, ClusterID: clusterID, Namespace: object.Namespace, Name: object.Name, NameKey: NameKeyV1(object.Name), Source: "k8s-boundary", SourceUID: uid, Status: "active", Confidence: 1, Generation: 1, AttrsVersion: 1, Attrs: object.Raw}
}

func dependencyEdge(relation, source, target, tenantID, clusterID, sourceField string) Edge {
	return Edge{EdgeUID: EdgeUID(tenantID, relation, source, target), SourceUID: source, TargetUID: target, RelationType: relation, TenantID: tenantID, ClusterID: clusterID, Status: "active", Source: "k8s-boundary", Confidence: 1, Generation: 1, CandidateDirection: CandidateDirection(relation), ImpactDirection: ImpactDirection(relation), AttrsVersion: 1, Attrs: map[string]interface{}{"source_field": sourceField, "fact_status": "fact"}}
}

func addEntity(entities *[]Entity, seen map[string]struct{}, entity Entity) {
	if entity.EntityUID == "" {
		return
	}
	if _, ok := seen[entity.EntityUID]; ok {
		return
	}
	seen[entity.EntityUID] = struct{}{}
	*entities = append(*entities, entity)
}

func addEdge(edges *[]Edge, seen map[string]struct{}, edge Edge) {
	if edge.EdgeUID == "" {
		return
	}
	if _, ok := seen[edge.EdgeUID]; ok {
		return
	}
	seen[edge.EdgeUID] = struct{}{}
	*edges = append(*edges, edge)
}

// ProjectKubeVirtDependencies converts the allow-listed Kubernetes snapshot
// into stable VM/VMI storage and network dependency identities. Missing
// optional objects are represented by a partial warning; default pod network
// interfaces intentionally produce no NAD edge.
func ProjectKubeVirtDependencies(snapshot map[string]interface{}, tenantID, clusterID string) (KubeVirtDependencyGraph, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clusterID) == "" {
		return KubeVirtDependencyGraph{}, errors.New("tenant and cluster are required")
	}
	result := KubeVirtDependencyGraph{}
	if partial, ok := snapshot["partial"].(bool); ok && partial {
		result.Partial = true
		result.WarningCodes = append(result.WarningCodes, "KUBERNETES_SNAPSHOT_PARTIAL")
	}
	entitiesSeen := map[string]struct{}{}
	edgesSeen := map[string]struct{}{}
	pvcIndex := indexObjects(snapshot, "pvcs")
	pvIndex := indexObjects(snapshot, "pvs")
	dvIndex := indexObjects(snapshot, "data_volumes")
	nadIndex := indexObjects(snapshot, "nads")
	for _, raw := range snapshotObjects(snapshot, "virtual_machine_instances") {
		vmi := objectRef(raw)
		if vmi.UID == "" || vmi.Name == "" {
			result.Partial = true
			continue
		}
		vmiEntity := objectEntity("vmi", clusterID, tenantID, vmi)
		addEntity(&result.Entities, entitiesSeen, vmiEntity)
		spec := kubeMapValue(raw["spec"])
		domain := kubeMapValue(spec["domain"])
		devices := kubeMapValue(domain["devices"])
		volumesByName := map[string]map[string]interface{}{}
		for _, value := range kubeListValue(domain["volumes"]) {
			volume := kubeMapValue(value)
			if name := kubeStringValue(volume["name"]); name != "" {
				volumesByName[name] = volume
			}
		}
		for _, value := range kubeListValue(devices["disks"]) {
			disk := kubeMapValue(value)
			deviceName := kubeStringValue(disk["name"])
			if deviceName == "" {
				continue
			}
			bus := kubeStringValue(kubeMapValue(disk["disk"])["bus"])
			diskUID := EntityUID("kubevirt-disk-device", clusterID, SHA256Parts(vmi.UID, deviceName))
			diskEntity := dependencyEntity("disk_device", diskUID, clusterID, tenantID, vmi.Namespace, deviceName, map[string]interface{}{"device_name": deviceName, "bus": bus})
			addEntity(&result.Entities, entitiesSeen, diskEntity)
			addEdge(&result.Edges, edgesSeen, dependencyEdge("USES_DISK", vmiEntity.EntityUID, diskEntity.EntityUID, tenantID, clusterID, "spec.domain.devices.disks[].name"))
			volume, ok := volumesByName[deviceName]
			if !ok {
				continue
			}
			volumeUID := EntityUID("kubevirt-volume", clusterID, SHA256Parts(vmi.UID, deviceName))
			volumeEntity := dependencyEntity("volume", volumeUID, clusterID, tenantID, vmi.Namespace, deviceName, map[string]interface{}{"volume_name": deviceName})
			addEntity(&result.Entities, entitiesSeen, volumeEntity)
			addEdge(&result.Edges, edgesSeen, dependencyEdge("REFERENCES_VOLUME", diskEntity.EntityUID, volumeEntity.EntityUID, tenantID, clusterID, "spec.domain.volumes[].name"))
			if dataVolume := kubeMapValue(volume["dataVolume"]); dataVolume != nil {
				name := kubeStringValue(dataVolume["name"])
				if name != "" {
					dv, ok := lookupObject(dvIndex, vmi.Namespace, name)
					if !ok {
						dv = kubeObjectRef{Name: name, Namespace: vmi.Namespace, UID: SHA256Parts("data_volume", vmi.Namespace, name)}
						result.Partial = true
					}
					dvEntity := objectEntity("data_volume", clusterID, tenantID, dv)
					addEntity(&result.Entities, entitiesSeen, dvEntity)
					addEdge(&result.Edges, edgesSeen, dependencyEdge("SOURCED_FROM", volumeEntity.EntityUID, dvEntity.EntityUID, tenantID, clusterID, "spec.domain.volumes[].dataVolume.name"))
					if dvRaw := kubeMapValue(dv.Raw["spec"]); dvRaw != nil {
						if sourcePVC := kubeMapValue(kubeMapValue(dvRaw["source"])["pvc"]); sourcePVC != nil {
							claim := kubeStringValue(sourcePVC["name"])
							if pvc, found := lookupObject(pvcIndex, vmi.Namespace, claim); found {
								pvcEntity := objectEntity("pvc", clusterID, tenantID, pvc)
								addEntity(&result.Entities, entitiesSeen, pvcEntity)
								addEdge(&result.Edges, edgesSeen, dependencyEdge("DECLARES", dvEntity.EntityUID, pvcEntity.EntityUID, tenantID, clusterID, "spec.source.pvc.name"))
								attachPV(&result, entitiesSeen, edgesSeen, pvc, pvcEntity, pvIndex, tenantID, clusterID)
							}
						}
					}
				}
			} else if claim := kubeStringValue(kubeMapValue(volume["persistentVolumeClaim"])["claimName"]); claim != "" {
				if pvc, found := lookupObject(pvcIndex, vmi.Namespace, claim); found {
					pvcEntity := objectEntity("pvc", clusterID, tenantID, pvc)
					addEntity(&result.Entities, entitiesSeen, pvcEntity)
					addEdge(&result.Edges, edgesSeen, dependencyEdge("DECLARES", volumeEntity.EntityUID, pvcEntity.EntityUID, tenantID, clusterID, "spec.domain.volumes[].persistentVolumeClaim.claimName"))
					attachPV(&result, entitiesSeen, edgesSeen, pvc, pvcEntity, pvIndex, tenantID, clusterID)
				}
			}
		}
		interfaces := kubeListValue(devices["interfaces"])
		networkRefs := map[string]map[string]interface{}{}
		for _, value := range kubeListValue(domain["networks"]) {
			network := kubeMapValue(value)
			if name := kubeStringValue(network["name"]); name != "" {
				networkRefs[name] = network
			}
		}
		for _, value := range interfaces {
			iface := kubeMapValue(value)
			name := kubeStringValue(iface["name"])
			if name == "" {
				continue
			}
			ifaceUID := EntityUID("kubevirt-interface", clusterID, SHA256Parts(vmi.UID, name))
			ifaceEntity := dependencyEntity("virtual_interface", ifaceUID, clusterID, tenantID, vmi.Namespace, name, map[string]interface{}{"interface_name": name, "binding": firstInterfaceBinding(iface)})
			addEntity(&result.Entities, entitiesSeen, ifaceEntity)
			network := networkRefs[name]
			if network == nil {
				continue
			}
			networkUID := EntityUID("kubevirt-network", clusterID, SHA256Parts(vmi.UID, name))
			networkEntity := dependencyEntity("network", networkUID, clusterID, tenantID, vmi.Namespace, name, map[string]interface{}{"network_name": name, "default_pod_network": mapValueHas(network, "pod")})
			addEntity(&result.Entities, entitiesSeen, networkEntity)
			addEdge(&result.Edges, edgesSeen, dependencyEdge("CONNECTS_TO_NETWORK", ifaceEntity.EntityUID, networkEntity.EntityUID, tenantID, clusterID, "spec.domain.networks[].name"))
			multus := kubeMapValue(network["multus"])
			nadName := kubeStringValue(multus["networkName"])
			if nadName == "" {
				// pod/default network: no NAD identity or edge is fabricated.
				continue
			}
			nadNamespace, nadResourceName := splitNamespacedName(nadName, vmi.Namespace)
			nad, found := lookupObject(nadIndex, nadNamespace, nadResourceName)
			if !found {
				result.Partial = true
				continue
			}
			nadEntity := objectEntity("nad", clusterID, tenantID, nad)
			addEntity(&result.Entities, entitiesSeen, nadEntity)
			addEdge(&result.Edges, edgesSeen, dependencyEdge("CONNECTS_TO_NAD", ifaceEntity.EntityUID, nadEntity.EntityUID, tenantID, clusterID, "spec.domain.networks[].multus.networkName"))
			config := kubeStringValue(kubeMapValue(nad.Raw["spec"])["config"])
			if config != "" {
				cniEntity := dependencyEntity("cni", EntityUID("cni", clusterID, SHA256Parts(nad.UID, config)), clusterID, tenantID, nad.Namespace, nad.Name+" CNI", map[string]interface{}{"config": config})
				addEntity(&result.Entities, entitiesSeen, cniEntity)
				addEdge(&result.Edges, edgesSeen, dependencyEdge("USES_CNI", nadEntity.EntityUID, cniEntity.EntityUID, tenantID, clusterID, "spec.config"))
			}
		}
	}
	sort.Slice(result.Entities, func(i, j int) bool { return result.Entities[i].EntityUID < result.Entities[j].EntityUID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].EdgeUID < result.Edges[j].EdgeUID })
	return result, nil
}

func mapValueHas(value map[string]interface{}, key string) bool {
	_, ok := value[key]
	return ok
}

func attachPV(result *KubeVirtDependencyGraph, entitiesSeen, edgesSeen map[string]struct{}, pvc kubeObjectRef, pvcEntity Entity, pvIndex map[string]kubeObjectRef, tenantID, clusterID string) {
	spec := kubeMapValue(pvc.Raw["spec"])
	if volumeName := kubeStringValue(spec["volumeName"]); volumeName != "" {
		if pv, ok := pvIndex[volumeName]; ok {
			pvEntity := objectEntity("pv", clusterID, tenantID, pv)
			addEntity(&result.Entities, entitiesSeen, pvEntity)
			addEdge(&result.Edges, edgesSeen, dependencyEdge("BOUND_TO", pvcEntity.EntityUID, pvEntity.EntityUID, tenantID, clusterID, "spec.volumeName"))
		}
	}
}

func firstInterfaceBinding(iface map[string]interface{}) string {
	for _, key := range []string{"bridge", "masquerade", "sriov", "slirp"} {
		if _, ok := iface[key]; ok {
			return key
		}
	}
	return "unknown"
}

func splitNamespacedName(value, fallbackNamespace string) (string, string) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return fallbackNamespace, value
}
