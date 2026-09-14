package graph

import "testing"

func TestKubeVirtDependencyProjectionKeepsCanonicalStorageIdentity(t *testing.T) {
	graph, err := ProjectKubeVirtDependencies(map[string]interface{}{
		"virtual_machine_instances": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-vmi-1", "name": "vm-1", "namespace": "prod"},
			"spec": map[string]interface{}{
				"domain": map[string]interface{}{
					"devices": map[string]interface{}{
						"disks": []interface{}{
							map[string]interface{}{"name": "rootdisk", "disk": map[string]interface{}{"bus": "virtio"}},
						},
						"interfaces": []interface{}{
							map[string]interface{}{"name": "default", "bridge": map[string]interface{}{}},
							map[string]interface{}{"name": "net1", "sriov": map[string]interface{}{}},
						},
					},
					"volumes": []interface{}{
						map[string]interface{}{"name": "rootdisk", "dataVolume": map[string]interface{}{"name": "dv-root"}},
					},
					"networks": []interface{}{
						map[string]interface{}{"name": "default", "pod": map[string]interface{}{}},
						map[string]interface{}{"name": "net1", "multus": map[string]interface{}{"networkName": "prod/nad-sriov-prod"}},
					},
				},
			},
		},
		},
		"data_volumes": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-dv-root", "name": "dv-root", "namespace": "prod"},
			"spec":     map[string]interface{}{"source": map[string]interface{}{"pvc": map[string]interface{}{"name": "pvc-root"}}},
		}},
		"pvcs": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-pvc-root", "name": "pvc-root", "namespace": "prod"},
			"spec":     map[string]interface{}{"volumeName": "pv-root"},
		}},
		"pvs": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-pv-root", "name": "pv-root"},
		}},
		"nads": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-nad", "name": "nad-sriov-prod", "namespace": "prod"},
			"spec":     map[string]interface{}{"config": "{\"cniVersion\":\"0.3.1\"}"},
		}},
	}, "tenant-a", "cluster-a")
	if err != nil {
		t.Fatalf("ProjectKubeVirtDependencies() error = %v", err)
	}

	pvcUID := K8sEntityUID("pvc", "cluster-a", "uid-pvc-root")
	if got := countEntitiesByUID(graph.Entities, pvcUID); got != 1 {
		t.Fatalf("PVC entity count = %d, want 1", got)
	}
	if !hasTypedPath(graph, []string{"vmi", "disk_device", "volume", "data_volume", "pvc", "pv"}, []string{"USES_DISK", "REFERENCES_VOLUME", "SOURCED_FROM", "DECLARES", "BOUND_TO"}) {
		t.Fatal("expected VMI -> disk -> volume -> DataVolume -> PVC -> PV path")
	}
	if !hasRelationEdge(graph, "virtual_interface", "CONNECTS_TO_NAD", "nad") {
		t.Fatal("expected Multus interface to NAD edge")
	}
	if !hasRelationEdge(graph, "virtual_interface", "CONNECTS_TO_NETWORK", "network") {
		t.Fatal("expected interface to logical network edge")
	}
	if hasEdgeWithSourceName(graph, "CONNECTS_TO_NAD", "default") {
		t.Fatal("default pod network must not fabricate a NAD edge")
	}
	for _, edge := range graph.Edges {
		if edge.RelationType == "CONNECTS_TO_NAD" && edge.Attrs["source_field"] != "spec.domain.networks[].multus.networkName" {
			t.Fatalf("NAD edge source_field = %v", edge.Attrs["source_field"])
		}
		if edge.Attrs["fact_status"] != "fact" {
			t.Fatalf("edge %s fact_status = %v, want fact", edge.RelationType, edge.Attrs["fact_status"])
		}
	}
}

func TestKubeVirtDependencyOntologyAcceptsTypedRelations(t *testing.T) {
	checks := [][3]string{
		{"USES_DISK", "vmi", "disk_device"},
		{"REFERENCES_VOLUME", "disk_device", "volume"},
		{"SOURCED_FROM", "volume", "data_volume"},
		{"DECLARES", "data_volume", "pvc"},
		{"BOUND_TO", "pvc", "pv"},
		{"CONNECTS_TO_NAD", "virtual_interface", "nad"},
		{"USES_CNI", "nad", "cni"},
	}
	for _, check := range checks {
		if err := ValidateRelation(check[0], check[1], check[2]); err != nil {
			t.Fatalf("ValidateRelation(%q, %q, %q) = %v", check[0], check[1], check[2], err)
		}
	}
}

func countEntitiesByUID(entities []Entity, uid string) int {
	count := 0
	for _, entity := range entities {
		if entity.EntityUID == uid {
			count++
		}
	}
	return count
}

func hasTypedPath(graph KubeVirtDependencyGraph, entityTypes []string, relations []string) bool {
	for i := range graph.Edges {
		for j := range graph.Edges {
			if graph.Edges[i].RelationType != relations[0] || graph.Edges[j].RelationType != relations[1] {
				continue
			}
			if entityTypeByUID(graph.Entities, graph.Edges[i].SourceUID) != entityTypes[0] || entityTypeByUID(graph.Entities, graph.Edges[i].TargetUID) != entityTypes[1] {
				continue
			}
			if graph.Edges[i].TargetUID != graph.Edges[j].SourceUID {
				continue
			}
			current := graph.Edges[j].TargetUID
			matched := 2
			for relationIndex := 2; relationIndex < len(relations); relationIndex++ {
				found := false
				for _, edge := range graph.Edges {
					if edge.SourceUID == current && edge.RelationType == relations[relationIndex] && entityTypeByUID(graph.Entities, edge.TargetUID) == entityTypes[relationIndex+1] {
						current = edge.TargetUID
						matched++
						found = true
						break
					}
				}
				if !found {
					break
				}
			}
			if matched == len(relations) {
				return true
			}
		}
	}
	return false
}

func entityTypeByUID(entities []Entity, uid string) string {
	for _, entity := range entities {
		if entity.EntityUID == uid {
			return entity.EntityType
		}
	}
	return ""
}

func hasRelationEdge(graph KubeVirtDependencyGraph, sourceType, relation, targetType string) bool {
	for _, edge := range graph.Edges {
		if edge.RelationType == relation && entityTypeByUID(graph.Entities, edge.SourceUID) == sourceType && entityTypeByUID(graph.Entities, edge.TargetUID) == targetType {
			return true
		}
	}
	return false
}

func hasEdgeWithSourceName(graph KubeVirtDependencyGraph, relation, name string) bool {
	for _, edge := range graph.Edges {
		if edge.RelationType != relation {
			continue
		}
		for _, entity := range graph.Entities {
			if entity.EntityUID == edge.SourceUID && entity.Name == name {
				return true
			}
		}
	}
	return false
}
