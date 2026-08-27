package graph

var candidateDirections = map[string]string{
	"REPRESENTS": "IN", "BACKED_BY": "OUT", "TARGETS": "OUT", "RUNS_ON": "OUT", "HOSTS": "IN",
	"HAS_COMPONENT": "OUT", "DEPENDS_ON": "OUT", "USES_VOLUME": "OUT", "BOUND_TO": "OUT", "ATTACHED_TO": "OUT", "INSTANCE_OF": "BOTH",
}

var impactDirections = map[string]string{
	"HAS_COMPONENT": "IN", "HOSTS": "OUT", "RUNS_ON": "IN", "BACKED_BY": "IN", "TARGETS": "IN",
	"REPRESENTS": "OUT", "DEPENDS_ON": "IN", "BELONGS_TO": "OUT", "USES_VOLUME": "IN", "BOUND_TO": "IN", "ATTACHED_TO": "IN",
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
