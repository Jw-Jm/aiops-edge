package graph

import (
	"context"
	"encoding/json"
)

// ShadowDiff is emitted for every sampled shadow comparison. A production
// writer can persist it to graph_shadow_diff_runs and expose counters without
// coupling this package to SQL.
type ShadowDiff struct {
	TenantID       string
	ScopeClusterID string
	SampleKind     string
	SampleCount    int
	MismatchCount  int
	Detail         interface{}
}

type ShadowRepository struct {
	primary   GraphRepository
	secondary GraphRepository
	record    func(ShadowDiff)
}

func NewShadowRepository(primary, secondary GraphRepository, record func(ShadowDiff)) *ShadowRepository {
	return &ShadowRepository{primary: primary, secondary: secondary, record: record}
}

func (r *ShadowRepository) GetEntity(ctx context.Context, scope GraphScope, uid string) (Entity, error) {
	entity, err := r.primary.GetEntity(ctx, scope, uid)
	if err != nil {
		return entity, err
	}
	shadowErr := r.compare(ctx, scope, "get_entity", func() (interface{}, error) { return r.secondary.GetEntity(ctx, scope, uid) }, entity)
	_ = shadowErr
	return entity, nil
}

func (r *ShadowRepository) SearchEntities(ctx context.Context, scope GraphScope, query EntitySearchQuery) ([]Entity, error) {
	items, err := r.primary.SearchEntities(ctx, scope, query)
	if err != nil {
		return items, err
	}
	r.compare(ctx, scope, "search_entities", func() (interface{}, error) { return r.secondary.SearchEntities(ctx, scope, query) }, items)
	return items, nil
}

func (r *ShadowRepository) Neighbors(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	result, err := r.primary.Neighbors(ctx, scope, query)
	if err != nil {
		return result, err
	}
	r.compare(ctx, scope, "neighbors", func() (interface{}, error) { return r.secondary.Neighbors(ctx, scope, query) }, result)
	return result, nil
}

func (r *ShadowRepository) ShortestPath(ctx context.Context, scope GraphScope, query PathQuery) (Subgraph, error) {
	result, err := r.primary.ShortestPath(ctx, scope, query)
	if err != nil {
		return result, err
	}
	r.compare(ctx, scope, "shortest_path", func() (interface{}, error) { return r.secondary.ShortestPath(ctx, scope, query) }, result)
	return result, nil
}

func (r *ShadowRepository) Impact(ctx context.Context, scope GraphScope, query ImpactQuery) (Subgraph, error) {
	result, err := r.primary.Impact(ctx, scope, query)
	if err != nil {
		return result, err
	}
	r.compare(ctx, scope, "impact", func() (interface{}, error) { return r.secondary.Impact(ctx, scope, query) }, result)
	return result, nil
}

func (r *ShadowRepository) CandidateSubgraph(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	result, err := r.primary.CandidateSubgraph(ctx, scope, query)
	if err != nil {
		return result, err
	}
	r.compare(ctx, scope, "candidate_subgraph", func() (interface{}, error) { return r.secondary.CandidateSubgraph(ctx, scope, query) }, result)
	return result, nil
}

func (r *ShadowRepository) BatchMutate(ctx context.Context, batch MutationBatch) (MutationResult, error) {
	result, err := r.primary.BatchMutate(ctx, batch)
	if err != nil {
		return result, err
	}
	if r.secondary == nil {
		r.recordDiff(batch.TenantID, batch.ClusterID, "batch_mutate", batchSize(batch), 1, "secondary repository is nil")
		return result, nil
	}
	secondaryResult, shadowErr := r.secondary.BatchMutate(ctx, batch)
	if shadowErr != nil {
		r.recordDiff(batch.TenantID, batch.ClusterID, "batch_mutate", batchSize(batch), 1, shadowErr.Error())
		return result, nil
	}
	if !equalJSON(result, secondaryResult) {
		r.recordDiff(batch.TenantID, batch.ClusterID, "batch_mutate", batchSize(batch), 1, map[string]interface{}{"primary": result, "secondary": secondaryResult})
	} else {
		r.recordDiff(batch.TenantID, batch.ClusterID, "batch_mutate", batchSize(batch), 0, nil)
	}
	return result, nil
}

func (r *ShadowRepository) Health(ctx context.Context) GraphHealth {
	if r.primary == nil {
		return GraphHealth{Ready: false, Backend: "shadow", SchemaVersion: GraphSchemaVersion, ErrorCode: ErrGraphUnavailable}
	}
	health := r.primary.Health(ctx)
	health.Backend = "shadow"
	return health
}

func (r *ShadowRepository) compare(_ context.Context, scope GraphScope, kind string, secondary func() (interface{}, error), primary interface{}) error {
	if r.secondary == nil {
		r.recordDiff(scope.TenantID, firstScopeCluster(scope), kind, 1, 1, "secondary repository is nil")
		return nil
	}
	value, err := secondary()
	if err != nil {
		r.recordDiff(scope.TenantID, firstScopeCluster(scope), kind, 1, 1, err.Error())
		return nil
	}
	if !equalJSON(primary, value) {
		r.recordDiff(scope.TenantID, firstScopeCluster(scope), kind, 1, 1, map[string]interface{}{"primary": primary, "secondary": value})
	} else {
		r.recordDiff(scope.TenantID, firstScopeCluster(scope), kind, 1, 0, nil)
	}
	return nil
}

func (r *ShadowRepository) recordDiff(tenantID, clusterID, kind string, sampleCount, mismatch int, detail interface{}) {
	if r.record != nil {
		r.record(ShadowDiff{TenantID: tenantID, ScopeClusterID: clusterID, SampleKind: kind, SampleCount: sampleCount, MismatchCount: mismatch, Detail: detail})
	}
}

func equalJSON(left, right interface{}) bool {
	left = normalizeShadowValue(left)
	right = normalizeShadowValue(right)
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func normalizeShadowValue(value interface{}) interface{} {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized interface{}
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return value
	}
	stripGeneratedAt(normalized)
	return normalized
}

func stripGeneratedAt(value interface{}) {
	switch item := value.(type) {
	case map[string]interface{}:
		delete(item, "generated_at")
		for _, child := range item {
			stripGeneratedAt(child)
		}
	case []interface{}:
		for _, child := range item {
			stripGeneratedAt(child)
		}
	}
}

func firstScopeCluster(scope GraphScope) string {
	for clusterID := range scope.ClusterIDs {
		return clusterID
	}
	return ""
}

func batchSize(batch MutationBatch) int { return len(batch.Vertices) + len(batch.Edges) }

var _ GraphRepository = (*ShadowRepository)(nil)
