package graph

var candidateDirections = map[string]string{
	"REPRESENTS": "IN", "BACKED_BY": "OUT", "TARGETS": "OUT", "RUNS_ON": "OUT", "HOSTS": "IN",
	"HAS_COMPONENT": "OUT", "DEPENDS_ON": "OUT", "USES_VOLUME": "OUT", "USES_DISK": "OUT", "REFERENCES_VOLUME": "OUT", "SOURCED_FROM": "OUT", "DECLARES": "OUT", "BOUND_TO": "OUT", "ATTACHED_TO": "OUT", "CONNECTS_TO_NAD": "OUT", "USES_CNI": "OUT", "CONNECTS_TO_NETWORK": "OUT", "INSTANCE_OF": "BOTH",
}

var impactDirections = map[string]string{
	"HAS_COMPONENT": "IN", "HOSTS": "OUT", "RUNS_ON": "IN", "BACKED_BY": "IN", "TARGETS": "IN",
	"REPRESENTS": "OUT", "DEPENDS_ON": "IN", "BELONGS_TO": "OUT", "USES_VOLUME": "IN", "USES_DISK": "IN", "REFERENCES_VOLUME": "IN", "SOURCED_FROM": "IN", "DECLARES": "IN", "BOUND_TO": "IN", "ATTACHED_TO": "IN", "CONNECTS_TO_NAD": "IN", "USES_CNI": "IN", "CONNECTS_TO_NETWORK": "IN",
}

func CandidateDirection(relation string) string {
	if direction, ok := candidateDirections[relation]; ok {
		return direction
	}
	return "NONE"
}

func ImpactDirection(relation string) string {
	if direction, ok := impactDirections[relation]; ok {
		return direction
	}
	return "NONE"
}
