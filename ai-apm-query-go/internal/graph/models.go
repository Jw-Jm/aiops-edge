package graph

import "time"

type Entity struct {
	EntityUID    string                 `json:"entity_uid"`
	EntityType   string                 `json:"entity_type"`
	TenantID     string                 `json:"tenant_id"`
	ClusterID    string                 `json:"cluster_id"`
	Namespace    string                 `json:"namespace,omitempty"`
	Name         string                 `json:"name"`
	NameKey      string                 `json:"name_key"`
	Source       string                 `json:"source"`
	SourceUID    string                 `json:"source_uid,omitempty"`
	Status       string                 `json:"status"`
	Health       string                 `json:"health,omitempty"`
	Resolution   string                 `json:"resolution,omitempty"`
	Confidence   float64                `json:"confidence"`
	FirstSeenMS  int64                  `json:"first_seen_ms"`
	LastSeenMS   int64                  `json:"last_seen_ms"`
	Generation   int64                  `json:"generation"`
	AttrsVersion int64                  `json:"attrs_version"`
	Attrs        map[string]interface{} `json:"attrs,omitempty"`
}

type Edge struct {
	EdgeUID            string                 `json:"edge_uid"`
	SourceUID          string                 `json:"source_uid"`
	TargetUID          string                 `json:"target_uid"`
	RelationType       string                 `json:"relation_type"`
	TenantID           string                 `json:"tenant_id"`
	ClusterID          string                 `json:"cluster_id"`
	Status             string                 `json:"status"`
	Source             string                 `json:"source"`
	Confidence         float64                `json:"confidence"`
	Generation         int64                  `json:"generation"`
	FirstSeenMS        int64                  `json:"first_seen_ms"`
	LastSeenMS         int64                  `json:"last_seen_ms"`
	ValidFromMS        int64                  `json:"valid_from_ms"`
	ValidToMS          int64                  `json:"valid_to_ms"`
	PropagatesFailure  bool                   `json:"propagates_failure"`
	CandidateDirection string                 `json:"candidate_direction"`
	ImpactDirection    string                 `json:"impact_direction"`
	AttrsVersion       int64                  `json:"attrs_version"`
	Attrs              map[string]interface{} `json:"attrs,omitempty"`
}

type GraphMeta struct {
	ContractVersion string `json:"contract_version"`
	SchemaVersion   int    `json:"schema_version"`
	// GraphGeneration binds a read snapshot to the newest source generation
	// represented by its returned vertices/edges.  RCA persists this value so
	// a replay can prove which graph projection generation it consumed.
	GraphGeneration int64    `json:"graph_generation"`
	Partial         bool     `json:"partial"`
	Stale           bool     `json:"stale"`
	GeneratedAt     string   `json:"generated_at"`
	WarningCodes    []string `json:"warning_codes"`
}

type Subgraph struct {
	CenterEntityUID string    `json:"center_entity_uid"`
	Vertices        []Entity  `json:"vertices"`
	Edges           []Edge    `json:"edges"`
	Meta            GraphMeta `json:"meta"`
}

func graphGeneration(vertices []Entity, edges []Edge) int64 {
	var generation int64
	for _, vertex := range vertices {
		if vertex.Generation > generation {
			generation = vertex.Generation
		}
	}
	for _, edge := range edges {
		if edge.Generation > generation {
			generation = edge.Generation
		}
	}
	return generation
}

func graphMeta(vertices []Entity, edges []Edge, partial bool, warningCodes []string, generatedAt string) GraphMeta {
	return GraphMeta{
		ContractVersion: GraphDTOContractVersion,
		SchemaVersion:   GraphSchemaVersion,
		GraphGeneration: graphGeneration(vertices, edges),
		Partial:         partial,
		GeneratedAt:     generatedAt,
		WarningCodes:    warningCodes,
	}
}

type MutationBatch struct {
	TenantID     string   `json:"tenant_id"`
	ClusterID    string   `json:"cluster_id"`
	Source       string   `json:"source"`
	Generation   int64    `json:"generation"`
	AttrsVersion int64    `json:"attrs_version"`
	Vertices     []Entity `json:"vertices"`
	Edges        []Edge   `json:"edges"`
}

type MutationResult struct {
	Accepted     int `json:"accepted"`
	Applied      int `json:"applied"`
	Idempotent   int `json:"idempotent"`
	SkippedStale int `json:"skipped_stale_version"`
	Conflicts    int `json:"conflicts"`
}

type GraphHealth struct {
	Ready          bool      `json:"ready"`
	Backend        string    `json:"backend"`
	SchemaVersion  int       `json:"schema_version"`
	SchemaChecksum string    `json:"schema_checksum,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	ErrorCode      string    `json:"error_code,omitempty"`
}
