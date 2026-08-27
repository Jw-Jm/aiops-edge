package graph

import "context"

// GenerationStaleMarker is implemented by graph backends that can perform
// the post-success generation transition.  It is intentionally separate from
// GraphRepository so read-only/legacy adapters cannot accidentally claim this
// destructive phase.
type GenerationStaleMarker interface {
	MarkStaleByGeneration(ctx context.Context, source, tenantID, clusterID string, generation int64) (int64, int64, error)
}

func (r *MemoryRepository) MarkStaleByGeneration(ctx context.Context, source, tenantID, clusterID string, generation int64) (int64, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var vertices, edges int64
	for uid, entity := range r.vertices {
		if entity.TenantID == tenantID && entity.ClusterID == clusterID && entity.Source == source && entity.Generation < generation && entity.Status != "stale" {
			entity.Status = "stale"
			r.vertices[uid] = entity
			vertices++
		}
	}
	for uid, edge := range r.edges {
		if edge.TenantID == tenantID && edge.ClusterID == clusterID && edge.Source == source && edge.Generation < generation && edge.Status != "stale" {
			edge.Status = "stale"
			r.edges[uid] = edge
			edges++
		}
	}
	return vertices, edges, nil
}

func (r *HugeGraphRepository) MarkStaleByGeneration(ctx context.Context, source, tenantID, clusterID string, generation int64) (int64, int64, error) {
	if r == nil || r.client == nil {
		return 0, 0, graphError(ErrGraphUnavailable, "HugeGraph client is not configured")
	}
	vertices, err := r.client.ListVertices(ctx)
	if err != nil {
		return 0, 0, graphError(ErrGraphUnavailable, err.Error())
	}
	updatedVertices := make([]Entity, 0)
	for _, raw := range vertices {
		entity, parseErr := entityFromHugeGraph(raw, "")
		if parseErr != nil || entity.TenantID != tenantID || entity.ClusterID != clusterID || entity.Source != source || entity.Generation >= generation || entity.Status == "stale" {
			continue
		}
		entity.Status = "stale"
		updatedVertices = append(updatedVertices, entity)
	}
	for start := 0; start < len(updatedVertices); start += 500 {
		end := start + 500
		if end > len(updatedVertices) {
			end = len(updatedVertices)
		}
		if err := r.client.PutVerticesBatch(ctx, updatedVertices[start:end]); err != nil {
			return 0, 0, graphError(ErrGraphUnavailable, err.Error())
		}
	}

	edges, err := r.client.ListEdges(ctx)
	if err != nil {
		return int64(len(updatedVertices)), 0, graphError(ErrGraphUnavailable, err.Error())
	}
	updatedEdges := make([]Edge, 0)
	for _, raw := range edges {
		edge, parseErr := edgeFromHugeGraph(raw)
		if parseErr != nil || edge.TenantID != tenantID || edge.ClusterID != clusterID || edge.Source != source || edge.Generation >= generation || edge.Status == "stale" {
			continue
		}
		edge.Status = "stale"
		updatedEdges = append(updatedEdges, edge)
	}
	for start := 0; start < len(updatedEdges); start += 500 {
		end := start + 500
		if end > len(updatedEdges) {
			end = len(updatedEdges)
		}
		if err := r.client.PutEdgesBatch(ctx, updatedEdges[start:end]); err != nil {
			return int64(len(updatedVertices)), 0, graphError(ErrGraphUnavailable, err.Error())
		}
	}
	return int64(len(updatedVertices)), int64(len(updatedEdges)), nil
}

func (r *ShadowRepository) MarkStaleByGeneration(ctx context.Context, source, tenantID, clusterID string, generation int64) (int64, int64, error) {
	if marker, ok := r.secondary.(GenerationStaleMarker); ok {
		return marker.MarkStaleByGeneration(ctx, source, tenantID, clusterID, generation)
	}
	return 0, 0, graphError(ErrGraphFeatureUnavailable, "shadow secondary has no generation marker")
}
