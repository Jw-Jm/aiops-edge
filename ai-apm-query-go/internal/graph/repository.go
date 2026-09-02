package graph

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultPublicMaxDepth      = 3
	DefaultPublicMaxVertices   = 300
	DefaultPublicMaxEdges      = 1000
	DefaultInternalMaxDepth    = 6
	DefaultInternalMaxVertices = 2000
	DefaultInternalMaxEdges    = 5000
	DefaultTraversalCapacity   = 20000
)

type GraphQueryLimits struct {
	MaxDepth    int
	MaxVertices int
	MaxEdges    int
	Capacity    int
}

func InternalGraphQueryLimits() GraphQueryLimits {
	return GraphQueryLimits{MaxDepth: DefaultInternalMaxDepth, MaxVertices: DefaultInternalMaxVertices, MaxEdges: DefaultInternalMaxEdges, Capacity: DefaultTraversalCapacity}
}

func PublicGraphQueryLimits() GraphQueryLimits {
	return GraphQueryLimits{MaxDepth: DefaultPublicMaxDepth, MaxVertices: DefaultPublicMaxVertices, MaxEdges: DefaultPublicMaxEdges, Capacity: DefaultTraversalCapacity}
}

func validateLimits(requestDepth, requestVertices, requestEdges int, limits GraphQueryLimits) error {
	if requestDepth <= 0 {
		requestDepth = 1
	}
	if requestVertices <= 0 {
		requestVertices = limits.MaxVertices
	}
	if requestEdges <= 0 {
		requestEdges = limits.MaxEdges
	}
	if requestDepth > limits.MaxDepth || requestVertices > limits.MaxVertices || requestEdges > limits.MaxEdges {
		return graphError(ErrGraphQueryLimitExceeded, "requested graph traversal exceeds server limit")
	}
	return nil
}

type EntitySearchQuery struct {
	EntityType string
	Name       string
	Limit      int
}

type NeighborQuery struct {
	CenterEntityUID string
	Direction       string
	MaxDepth        int
	MaxVertices     int
	MaxEdges        int
	RelationTypes   []string
}

type PathQuery struct {
	SourceUID     string
	TargetUID     string
	MaxDepth      int
	MaxVertices   int
	MaxEdges      int
	RelationTypes []string
}

type ImpactQuery struct {
	RootUID     string
	MaxDepth    int
	MaxVertices int
	MaxEdges    int
}

type GraphRepository interface {
	GetEntity(context.Context, GraphScope, string) (Entity, error)
	SearchEntities(context.Context, GraphScope, EntitySearchQuery) ([]Entity, error)
	Neighbors(context.Context, GraphScope, NeighborQuery) (Subgraph, error)
	ShortestPath(context.Context, GraphScope, PathQuery) (Subgraph, error)
	Impact(context.Context, GraphScope, ImpactQuery) (Subgraph, error)
	CandidateSubgraph(context.Context, GraphScope, NeighborQuery) (Subgraph, error)
	BatchMutate(context.Context, MutationBatch) (MutationResult, error)
	Health(context.Context) GraphHealth
}

type MemoryRepository struct {
	mu       sync.RWMutex
	vertices map[string]Entity
	edges    map[string]Edge
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{vertices: map[string]Entity{}, edges: map[string]Edge{}}
}

func (r *MemoryRepository) GetEntity(ctx context.Context, scope GraphScope, uid string) (Entity, error) {
	if err := ctx.Err(); err != nil {
		return Entity{}, err
	}
	r.mu.RLock()
	entity, ok := r.vertices[uid]
	r.mu.RUnlock()
	if !ok {
		return Entity{}, graphError(ErrGraphEntityNotFound, uid)
	}
	if !scope.Allows(entity) {
		return Entity{}, graphError(ErrGraphScopeViolation, uid)
	}
	return entity, nil
}

func (r *MemoryRepository) SearchEntities(ctx context.Context, scope GraphScope, query EntitySearchQuery) ([]Entity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 300 {
		return nil, graphError(ErrGraphQueryLimitExceeded, "search limit exceeds 300")
	}
	name := strings.ToLower(strings.TrimSpace(query.Name))
	r.mu.RLock()
	out := make([]Entity, 0)
	for _, entity := range r.vertices {
		if !scope.Allows(entity) || (query.EntityType != "" && entity.EntityType != query.EntityType) {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(entity.Name), name) {
			continue
		}
		out = append(out, entity)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].NameKey < out[j].NameKey })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *MemoryRepository) Neighbors(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	limits := InternalGraphQueryLimits()
	if err := validateLimits(query.MaxDepth, query.MaxVertices, query.MaxEdges, limits); err != nil {
		return Subgraph{}, err
	}
	if query.MaxDepth <= 0 {
		query.MaxDepth = 1
	}
	if query.MaxVertices <= 0 {
		query.MaxVertices = limits.MaxVertices
	}
	if query.MaxEdges <= 0 {
		query.MaxEdges = limits.MaxEdges
	}
	return r.walk(ctx, scope, query.CenterEntityUID, query.MaxDepth, query.MaxVertices, query.MaxEdges, query.RelationTypes, query.Direction, nil)
}

func (r *MemoryRepository) ShortestPath(ctx context.Context, scope GraphScope, query PathQuery) (Subgraph, error) {
	limits := InternalGraphQueryLimits()
	if err := validateLimits(query.MaxDepth, query.MaxVertices, query.MaxEdges, limits); err != nil {
		return Subgraph{}, err
	}
	if query.MaxDepth <= 0 {
		query.MaxDepth = limits.MaxDepth
	}
	if query.MaxVertices <= 0 {
		query.MaxVertices = limits.MaxVertices
	}
	if query.MaxEdges <= 0 {
		query.MaxEdges = limits.MaxEdges
	}
	if _, err := r.GetEntity(ctx, scope, query.SourceUID); err != nil {
		return Subgraph{}, err
	}
	if _, err := r.GetEntity(ctx, scope, query.TargetUID); err != nil {
		return Subgraph{}, err
	}
	r.mu.RLock()
	queue := []string{query.SourceUID}
	parents := map[string]string{query.SourceUID: ""}
	depths := map[string]int{query.SourceUID: 0}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == query.TargetUID {
			break
		}
		if depths[current] >= query.MaxDepth {
			continue
		}
		for _, edge := range r.edges {
			if !relationAllowed(edge.RelationType, query.RelationTypes) {
				continue
			}
			source, sourceOK := r.vertices[edge.SourceUID]
			target, targetOK := r.vertices[edge.TargetUID]
			if !sourceOK || !targetOK || !scope.AllowsEdge(source, target) {
				continue
			}
			next := ""
			if edge.SourceUID == current {
				next = edge.TargetUID
			} else if edge.TargetUID == current {
				next = edge.SourceUID
			}
			if next == "" {
				continue
			}
			if _, seen := parents[next]; seen {
				continue
			}
			parents[next] = current
			depths[next] = depths[current] + 1
			queue = append(queue, next)
		}
	}
	r.mu.RUnlock()
	if _, ok := parents[query.TargetUID]; !ok {
		return Subgraph{}, graphError(ErrGraphEmpty, "no path")
	}
	vertices := []Entity{}
	edges := []Edge{}
	for uid := query.TargetUID; uid != ""; uid = parents[uid] {
		entity, err := r.GetEntity(ctx, scope, uid)
		if err != nil {
			return Subgraph{}, err
		}
		vertices = append(vertices, entity)
		if parent := parents[uid]; parent != "" {
			r.mu.RLock()
			for _, edge := range r.edges {
				if (edge.SourceUID == uid && edge.TargetUID == parent) || (edge.SourceUID == parent && edge.TargetUID == uid) {
					edges = append(edges, edge)
					break
				}
			}
			r.mu.RUnlock()
		}
	}
	reverseEntities(vertices)
	return r.subgraph(query.SourceUID, vertices, edges), nil
}

func (r *MemoryRepository) Impact(ctx context.Context, scope GraphScope, query ImpactQuery) (Subgraph, error) {
	limits := InternalGraphQueryLimits()
	if err := validateLimits(query.MaxDepth, query.MaxVertices, query.MaxEdges, limits); err != nil {
		return Subgraph{}, err
	}
	if query.MaxDepth <= 0 {
		query.MaxDepth = limits.MaxDepth
	}
	if query.MaxVertices <= 0 {
		query.MaxVertices = limits.MaxVertices
	}
	if query.MaxEdges <= 0 {
		query.MaxEdges = limits.MaxEdges
	}
	return r.walk(ctx, scope, query.RootUID, query.MaxDepth, query.MaxVertices, query.MaxEdges, impactRelationTypes(), "BOTH", impactDirections)
}

func (r *MemoryRepository) CandidateSubgraph(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	limits := InternalGraphQueryLimits()
	if err := validateLimits(query.MaxDepth, query.MaxVertices, query.MaxEdges, limits); err != nil {
		return Subgraph{}, err
	}
	if query.MaxDepth <= 0 {
		query.MaxDepth = limits.MaxDepth
	}
	if query.MaxVertices <= 0 {
		query.MaxVertices = limits.MaxVertices
	}
	if query.MaxEdges <= 0 {
		query.MaxEdges = limits.MaxEdges
	}
	relations := query.RelationTypes
	if len(relations) == 0 {
		relations = candidateRelationTypes()
	}
	return r.walk(ctx, scope, query.CenterEntityUID, query.MaxDepth, query.MaxVertices, query.MaxEdges, relations, "BOTH", candidateDirections)
}

func (r *MemoryRepository) BatchMutate(ctx context.Context, batch MutationBatch) (MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{Accepted: len(batch.Vertices) + len(batch.Edges)}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entity := range batch.Vertices {
		if err := ValidateEntityType(entity.EntityType); err != nil {
			return result, err
		}
		if batch.TenantID != "" && entity.TenantID != batch.TenantID {
			return result, graphError(ErrGraphScopeViolation, entity.EntityUID)
		}
		if old, ok := r.vertices[entity.EntityUID]; ok {
			if old.AttrsVersion > entity.AttrsVersion {
				result.SkippedStale++
				continue
			}
			if old.AttrsVersion == entity.AttrsVersion {
				if !reflect.DeepEqual(old, entity) {
					return result, graphError(ErrGraphVersionConflict, entity.EntityUID)
				}
				result.Idempotent++
				continue
			}
		}
		r.vertices[entity.EntityUID] = entity
		result.Applied++
	}
	for _, edge := range batch.Edges {
		source, sourceOK := r.vertices[edge.SourceUID]
		target, targetOK := r.vertices[edge.TargetUID]
		if !sourceOK || !targetOK {
			return result, graphError(ErrGraphEntityNotFound, edge.SourceUID+"/"+edge.TargetUID)
		}
		if err := ValidateRelation(edge.RelationType, source.EntityType, target.EntityType); err != nil {
			return result, err
		}
		if batch.TenantID != "" && (edge.TenantID != batch.TenantID || source.TenantID != batch.TenantID || target.TenantID != batch.TenantID) {
			return result, graphError(ErrGraphScopeViolation, edge.EdgeUID)
		}
		if old, ok := r.edges[edge.EdgeUID]; ok {
			if old.AttrsVersion > edge.AttrsVersion {
				result.SkippedStale++
				continue
			}
			if old.AttrsVersion == edge.AttrsVersion {
				if !reflect.DeepEqual(old, edge) {
					return result, graphError(ErrGraphVersionConflict, edge.EdgeUID)
				}
				result.Idempotent++
				continue
			}
		}
		r.edges[edge.EdgeUID] = edge
		result.Applied++
	}
	return result, nil
}

func (r *MemoryRepository) Health(context.Context) GraphHealth {
	return GraphHealth{Ready: true, Backend: "memory", SchemaVersion: GraphSchemaVersion, CheckedAt: time.Now().UTC()}
}

func (r *MemoryRepository) DeleteEntity(ctx context.Context, scope GraphScope, uid string) error {
	if _, err := r.GetEntity(ctx, scope, uid); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.vertices, uid)
	for edgeUID, edge := range r.edges {
		if edge.SourceUID == uid || edge.TargetUID == uid {
			delete(r.edges, edgeUID)
		}
	}
	r.mu.Unlock()
	return nil
}

func (r *MemoryRepository) DeleteEdge(ctx context.Context, scope GraphScope, uid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	edge, ok := r.edges[uid]
	if ok {
		source, sourceOK := r.vertices[edge.SourceUID]
		target, targetOK := r.vertices[edge.TargetUID]
		if !sourceOK || !targetOK || !scope.AllowsEdge(source, target) {
			r.mu.Unlock()
			return graphError(ErrGraphScopeViolation, uid)
		}
		delete(r.edges, uid)
	}
	r.mu.Unlock()
	if !ok {
		return graphError(ErrGraphEntityNotFound, uid)
	}
	return nil
}

func (r *MemoryRepository) walk(ctx context.Context, scope GraphScope, center string, maxDepth, maxVertices, maxEdges int, relationTypes []string, direction string, policy map[string]string) (Subgraph, error) {
	centerEntity, err := r.GetEntity(ctx, scope, center)
	if err != nil {
		return Subgraph{}, err
	}
	vertices := map[string]Entity{centerEntity.EntityUID: centerEntity}
	edges := map[string]Edge{}
	queue := []struct {
		uid   string
		depth int
	}{{center, 0}}
	r.mu.RLock()
	for len(queue) > 0 && len(vertices) < maxVertices && len(edges) < maxEdges {
		if err := ctx.Err(); err != nil {
			r.mu.RUnlock()
			return Subgraph{}, err
		}
		item := queue[0]
		queue = queue[1:]
		if item.depth >= maxDepth {
			continue
		}
		for _, edge := range r.edges {
			if !relationAllowed(edge.RelationType, relationTypes) {
				continue
			}
			next, ok := traversalNext(edge, item.uid, direction, policy)
			if !ok {
				continue
			}
			source, sourceOK := r.vertices[edge.SourceUID]
			target, targetOK := r.vertices[edge.TargetUID]
			if !sourceOK || !targetOK || !scope.AllowsEdge(source, target) {
				continue
			}
			if _, seen := vertices[next]; !seen && len(vertices) >= maxVertices {
				break
			}
			edges[edge.EdgeUID] = edge
			if _, seen := vertices[next]; !seen {
				vertices[next] = r.vertices[next]
				queue = append(queue, struct {
					uid   string
					depth int
				}{next, item.depth + 1})
			}
			if len(edges) >= maxEdges {
				break
			}
		}
	}
	r.mu.RUnlock()
	if len(edges) >= maxEdges || len(vertices) >= maxVertices {
		vertexList, edgeList := mapEntities(vertices), mapEdges(edges)
		return Subgraph{CenterEntityUID: center, Vertices: vertexList, Edges: edgeList,
			Meta: graphMeta(vertexList, edgeList, true, []string{ErrGraphQueryLimitExceeded}, time.Now().UTC().Format(time.RFC3339Nano))}, nil
	}
	vertexList, edgeList := mapEntities(vertices), mapEdges(edges)
	return Subgraph{CenterEntityUID: center, Vertices: vertexList, Edges: edgeList,
		Meta: graphMeta(vertexList, edgeList, false, []string{}, time.Now().UTC().Format(time.RFC3339Nano))}, nil
}

func traversalNext(edge Edge, current, direction string, policy map[string]string) (string, bool) {
	requested := strings.ToUpper(strings.TrimSpace(direction))
	if policy != nil {
		requested = policy[edge.RelationType]
	}
	if requested == "" {
		requested = "BOTH"
	}
	switch requested {
	case "OUT":
		if edge.SourceUID == current {
			return edge.TargetUID, true
		}
	case "IN":
		if edge.TargetUID == current {
			return edge.SourceUID, true
		}
	case "BOTH":
		if edge.SourceUID == current {
			return edge.TargetUID, true
		}
		if edge.TargetUID == current {
			return edge.SourceUID, true
		}
	}
	return "", false
}

func (r *MemoryRepository) subgraph(center string, vertices []Entity, edges []Edge) Subgraph {
	return Subgraph{CenterEntityUID: center, Vertices: vertices, Edges: edges,
		Meta: graphMeta(vertices, edges, false, []string{}, time.Now().UTC().Format(time.RFC3339Nano))}
}

func relationAllowed(relation string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if relation == item {
			return true
		}
	}
	return false
}
func mapEntities(items map[string]Entity) []Entity {
	out := make([]Entity, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityUID < out[j].EntityUID })
	return out
}
func mapEdges(items map[string]Edge) []Edge {
	out := make([]Edge, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeUID < out[j].EdgeUID })
	return out
}
func reverseEntities(items []Entity) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

var _ GraphRepository = (*MemoryRepository)(nil)

// Compatibility aliases keep the scheme vocabulary available to callers while
// GraphRepository remains the concrete DTO contract used by this repository.
type Vertex = Entity
type Scope = GraphScope
type Health = GraphHealth
type Repository = GraphRepository
