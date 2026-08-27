"""The single graph-ontology-v2 allow-list."""
from __future__ import annotations

ENTITY_TYPES = frozenset({
    "business", "application", "service", "middleware", "k8s_cluster", "namespace", "k8s_node",
    "deployment", "replicaset", "statefulset", "daemonset", "pod", "container", "k8s_service",
    "endpoint_slice", "pvc", "pv", "storage_class", "nad", "network", "vm", "vmi", "migration",
    "physical_server", "cpu", "dimm", "nic", "disk", "mainboard", "bmc", "psu", "fan", "switch",
    "switch_port", "alert", "change", "case", "sel_event",
})

RELATION_PAIRS = {
    "HAS_COMPONENT": {"physical_server": {"cpu", "dimm", "nic", "disk", "mainboard", "bmc", "psu", "fan"}},
    "HOSTS": {"physical_server": {"k8s_node"}},
    "CONTAINS": {"k8s_cluster": {"namespace", "k8s_node"}},
    "OWNS": {"deployment": {"replicaset"}, "replicaset": {"pod"}, "statefulset": {"pod"}, "daemonset": {"pod"}},
    "INSTANCE_OF": {"vmi": {"vm"}},
    "RUNS_ON": {"pod": {"k8s_node"}, "vmi": {"k8s_node"}},
    "TARGETS": {"k8s_service": {"endpoint_slice"}},
    "BACKED_BY": {"endpoint_slice": {"pod"}},
    "REPRESENTS": {"k8s_service": {"service"}, "deployment": {"service"}, "statefulset": {"service"}, "daemonset": {"service"}},
    "USES_VOLUME": {"pod": {"pvc"}, "vm": {"pvc"}},
    "BOUND_TO": {"pvc": {"pv"}},
    "ATTACHED_TO": {"pod": {"nad", "network"}, "vm": {"nad", "network"}},
    "CONNECTS_TO": {"nic": {"switch_port"}, "switch_port": {"switch_port", "switch"}, "switch": {"switch_port", "switch"}},
    "DEPENDS_ON": {"service": {"service", "middleware"}},
    "BELONGS_TO": {"service": {"application"}, "application": {"business"}, "pod": {"vmi"}},
    "HAS_CHANGE": {t: {"change"} for t in ENTITY_TYPES if t not in {"change", "case", "alert", "sel_event"}},
    "RAISES": {t: {"alert"} for t in ENTITY_TYPES if t not in {"alert", "case", "sel_event"}},
    "CAUSED_BY": {"alert": set(ENTITY_TYPES), "case": set(ENTITY_TYPES)},
    "MENTIONED_IN": {t: {"case"} for t in ENTITY_TYPES if t not in {"case", "alert", "sel_event"}},
}


def validate_entity_type(entity_type: str) -> str:
    value = (entity_type or "").strip().lower()
    if value not in ENTITY_TYPES:
        raise ValueError("GRAPH_UNKNOWN_ENTITY_TYPE")
    return value


def validate_relation(relation: str, source_type: str, target_type: str) -> str:
    relation = (relation or "").strip().upper()
    source_type, target_type = validate_entity_type(source_type), validate_entity_type(target_type)
    if target_type not in RELATION_PAIRS.get(relation, {}).get(source_type, set()):
        raise ValueError("GRAPH_ONTOLOGY_VIOLATION")
    return relation
