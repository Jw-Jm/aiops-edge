package graph

import (
	"sort"
	"strings"
)

var entityTypes = map[string]struct{}{
	"business": {}, "application": {}, "service": {}, "middleware": {},
	"k8s_cluster": {}, "namespace": {}, "k8s_node": {}, "deployment": {},
	"replicaset": {}, "statefulset": {}, "daemonset": {}, "pod": {}, "container": {},
	"k8s_service": {}, "endpoint_slice": {}, "pvc": {}, "pv": {}, "storage_class": {}, "nad": {}, "network": {},
	"vm": {}, "vmi": {}, "migration": {},
	"physical_server": {}, "cpu": {}, "dimm": {}, "nic": {}, "disk": {}, "mainboard": {}, "bmc": {}, "psu": {}, "fan": {},
	"switch": {}, "switch_port": {}, "alert": {}, "change": {}, "case": {}, "sel_event": {},
}

var relations = map[string]map[string]struct{}{
	"HAS_COMPONENT": {"physical_server": {}, "cpu": {}, "dimm": {}, "nic": {}, "disk": {}, "mainboard": {}, "bmc": {}, "psu": {}, "fan": {}},
	"HOSTS":         {"physical_server": {}, "k8s_node": {}},
	"CONTAINS":      {"k8s_cluster": {}, "namespace": {}, "k8s_node": {}},
	"OWNS":          {"deployment": {}, "replicaset": {}, "statefulset": {}, "daemonset": {}, "pod": {}},
	"INSTANCE_OF":   {"vmi": {}, "vm": {}},
	"RUNS_ON":       {"pod": {}, "vmi": {}, "k8s_node": {}},
	"TARGETS":       {"k8s_service": {}, "endpoint_slice": {}},
	"BACKED_BY":     {"endpoint_slice": {}, "pod": {}},
	"REPRESENTS":    {"k8s_service": {}, "deployment": {}, "statefulset": {}, "daemonset": {}, "service": {}},
	"USES_VOLUME":   {"pod": {}, "vm": {}, "pvc": {}},
	"BOUND_TO":      {"pvc": {}, "pv": {}},
	"ATTACHED_TO":   {"pod": {}, "vm": {}, "nad": {}, "network": {}},
	"CONNECTS_TO":   {"nic": {}, "switch_port": {}, "switch": {}},
	"DEPENDS_ON":    {"service": {}, "middleware": {}},
	"BELONGS_TO":    {"service": {}, "application": {}, "business": {}, "pod": {}},
	"HAS_CHANGE":    {"change": {}},
	"RAISES":        {"alert": {}},
	"CAUSED_BY":     {"alert": {}, "case": {}},
	"MENTIONED_IN":  {"case": {}},
}

var relationPairs = map[string]map[string]map[string]struct{}{
	"HAS_COMPONENT": {
		"physical_server": {"cpu": {}, "dimm": {}, "nic": {}, "disk": {}, "mainboard": {}, "bmc": {}, "psu": {}, "fan": {}},
	},
	"HOSTS":        {"physical_server": {"k8s_node": {}}},
	"CONTAINS":     {"k8s_cluster": {"namespace": {}, "k8s_node": {}}},
	"OWNS":         {"deployment": {"replicaset": {}}, "replicaset": {"pod": {}}, "statefulset": {"pod": {}}, "daemonset": {"pod": {}}},
	"INSTANCE_OF":  {"vmi": {"vm": {}}},
	"RUNS_ON":      {"pod": {"k8s_node": {}}, "vmi": {"k8s_node": {}}},
	"TARGETS":      {"k8s_service": {"endpoint_slice": {}}},
	"BACKED_BY":    {"endpoint_slice": {"pod": {}}},
	"REPRESENTS":   {"k8s_service": {"service": {}}, "deployment": {"service": {}}, "statefulset": {"service": {}}, "daemonset": {"service": {}}},
	"USES_VOLUME":  {"pod": {"pvc": {}}, "vm": {"pvc": {}}},
	"BOUND_TO":     {"pvc": {"pv": {}}},
	"ATTACHED_TO":  {"pod": {"nad": {}, "network": {}}, "vm": {"nad": {}, "network": {}}},
	"CONNECTS_TO":  {"nic": {"switch_port": {}}, "switch_port": {"switch_port": {}, "switch": {}}, "switch": {"switch_port": {}, "switch": {}}},
	"DEPENDS_ON":   {"service": {"service": {}, "middleware": {}}},
	"BELONGS_TO":   {"service": {"application": {}}, "application": {"business": {}}, "pod": {"vmi": {}}},
	"HAS_CHANGE":   {"business": {"change": {}}, "application": {"change": {}}, "service": {"change": {}}, "middleware": {"change": {}}, "pod": {"change": {}}, "k8s_node": {"change": {}}, "vm": {"change": {}}, "vmi": {"change": {}}, "physical_server": {"change": {}}, "cpu": {"change": {}}, "dimm": {"change": {}}, "nic": {"change": {}}, "disk": {"change": {}}, "mainboard": {"change": {}}, "bmc": {"change": {}}, "psu": {"change": {}}, "fan": {"change": {}}},
	"RAISES":       {"business": {"alert": {}}, "application": {"alert": {}}, "service": {"alert": {}}, "middleware": {"alert": {}}, "pod": {"alert": {}}, "k8s_node": {"alert": {}}, "vm": {"alert": {}}, "vmi": {"alert": {}}, "physical_server": {"alert": {}}, "dimm": {"alert": {}}},
	"CAUSED_BY":    {"alert": {"business": {}, "application": {}, "service": {}, "middleware": {}, "pod": {}, "k8s_node": {}, "vm": {}, "vmi": {}, "physical_server": {}, "dimm": {}, "change": {}}, "case": {"business": {}, "application": {}, "service": {}, "middleware": {}, "pod": {}, "k8s_node": {}, "vm": {}, "vmi": {}, "physical_server": {}, "dimm": {}, "change": {}}},
	"MENTIONED_IN": {"business": {"case": {}}, "application": {"case": {}}, "service": {"case": {}}, "middleware": {"case": {}}, "pod": {"case": {}}, "k8s_node": {"case": {}}, "vm": {"case": {}}, "vmi": {"case": {}}, "physical_server": {"case": {}}, "dimm": {"case": {}}, "change": {"case": {}}},
}

func ValidateEntityType(entityType string) error {
	if _, ok := entityTypes[strings.ToLower(strings.TrimSpace(entityType))]; !ok {
		return graphError(ErrUnknownEntityType, entityType)
	}
	return nil
}

func ValidateRelation(relation, sourceType, targetType string) error {
	relation = strings.ToUpper(strings.TrimSpace(relation))
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	targetType = strings.ToLower(strings.TrimSpace(targetType))
	if err := ValidateEntityType(sourceType); err != nil {
		return err
	}
	if err := ValidateEntityType(targetType); err != nil {
		return err
	}
	pairs, ok := relationPairs[relation]
	if !ok {
		return graphError(ErrOntologyViolation, relation)
	}
	if targets, ok := pairs[sourceType]; ok {
		if _, ok := targets[targetType]; ok {
			return nil
		}
	}
	return graphError(ErrOntologyViolation, relation+" "+sourceType+" -> "+targetType)
}

func RelationTypes() []string {
	out := make([]string, 0, len(relations))
	for name := range relations {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
