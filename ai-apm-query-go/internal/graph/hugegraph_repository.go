package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HugeGraphRepository is the production graph read/write adapter. It only
// exposes typed graph operations; callers never receive a raw Gremlin client.
type HugeGraphRepository struct {
	client *HugeGraphClient
}

func NewHugeGraphRepository(client *HugeGraphClient) *HugeGraphRepository {
	return &HugeGraphRepository{client: client}
}

func (r *HugeGraphRepository) GetEntity(ctx context.Context, scope GraphScope, uid string) (Entity, error) {
	if r == nil || r.client == nil {
		return Entity{}, graphError(ErrGraphUnavailable, "HugeGraph client is not configured")
	}
	raw, err := r.client.GetVertex(ctx, uid)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return Entity{}, graphError(ErrGraphEntityNotFound, uid)
		}
		return Entity{}, graphError(ErrGraphUnavailable, err.Error())
	}
	entity, err := entityFromHugeGraph(raw, uid)
	if err != nil {
		return Entity{}, err
	}
	if !scope.Allows(entity) {
		return Entity{}, graphError(ErrGraphScopeViolation, uid)
	}
	return entity, nil
}

func (r *HugeGraphRepository) SearchEntities(ctx context.Context, scope GraphScope, query EntitySearchQuery) ([]Entity, error) {
	// Name lookup must go through graph_entity_alias in MySQL. HugeGraph has no
	// permitted full name scan endpoint, so this backend deliberately refuses
	// the unsafe operation instead of silently scanning the graph.
	return nil, graphError(ErrGraphFeatureUnavailable, "HugeGraph entity search requires graph_entity_alias")
}

func (r *HugeGraphRepository) Neighbors(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
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
	center, err := r.GetEntity(ctx, scope, query.CenterEntityUID)
	if err != nil {
		return Subgraph{}, err
	}
	raw, err := r.client.KNeighbor(ctx, KNeighborRequest{
		Source: query.CenterEntityUID, Direction: normalizedDirection(query.Direction), MaxDepth: query.MaxDepth, Limit: query.MaxVertices,
		Capacity: limits.Capacity, Nearest: true, WithVertex: true, WithPath: true,
		WithEdge: true, EdgeLabels: normalizedRelationLabels(query.RelationTypes),
	})
	if err != nil {
		return Subgraph{}, graphError(ErrGraphUnavailable, err.Error())
	}
	// HugeGraph 1.7 may return an empty K-neighbor result for a valid
	// CUSTOMIZE_STRING vertex ID.  The indexed vertex_id edge endpoint remains
	// authoritative and bounded; use it only when the traverser produced no
	// traversable object so RCA does not silently degrade to a single-node graph.
	if len(interfaceSlice(raw["edges"])) == 0 && len(interfaceSlice(raw["vertices"])) == 0 && len(interfaceSlice(raw["paths"])) == 0 {
		return r.neighborsFromIndexedEdges(ctx, scope, center, query)
	}
	return r.subgraphFromTraverser(ctx, scope, center.EntityUID, raw, query.MaxVertices, query.MaxEdges)
}

func (r *HugeGraphRepository) neighborsFromIndexedEdges(ctx context.Context, scope GraphScope, center Entity, query NeighborQuery) (Subgraph, error) {
	labels := normalizedRelationLabels(query.RelationTypes)
	rawEdges, err := r.client.EdgesForVertex(ctx, center.EntityUID, query.Direction, labels)
	if err != nil {
		return Subgraph{}, graphError(ErrGraphUnavailable, err.Error())
	}
	vertices := []Entity{center}
	seenVertices := map[string]struct{}{center.EntityUID: {}}
	capacity := len(rawEdges)
	if capacity > query.MaxEdges {
		capacity = query.MaxEdges
	}
	edges := make([]Edge, 0, capacity)
	for _, rawEdge := range rawEdges {
		if len(edges) >= query.MaxEdges {
			break
		}
		edge, edgeErr := edgeFromHugeGraph(rawEdge)
		if edgeErr != nil {
			return Subgraph{}, edgeErr
		}
		if edge.SourceUID != center.EntityUID && edge.TargetUID != center.EntityUID {
			continue
		}
		if edge.TenantID != "" && edge.TenantID != scope.TenantID {
			continue
		}
		if edge.ClusterID != "" && !scope.Allows(Entity{TenantID: scope.TenantID, ClusterID: edge.ClusterID}) {
			continue
		}
		if len(labels) > 0 && !containsString(labels, strings.ToUpper(edge.RelationType)) {
			continue
		}
		neighborUID := edge.TargetUID
		if neighborUID == center.EntityUID {
			neighborUID = edge.SourceUID
		}
		if neighborUID == "" || neighborUID == center.EntityUID {
			continue
		}
		neighbor, ok := findEntity(vertices, neighborUID)
		if !ok {
			neighbor, err = r.GetEntity(ctx, scope, neighborUID)
			if err != nil {
				return Subgraph{}, err
			}
		}
		if !scope.Allows(neighbor) {
			return Subgraph{}, graphError(ErrGraphScopeViolation, neighborUID)
		}
		if _, seen := seenVertices[neighbor.EntityUID]; !seen {
			if len(vertices) >= query.MaxVertices {
				break
			}
			vertices = append(vertices, neighbor)
			seenVertices[neighbor.EntityUID] = struct{}{}
		}
		edges = append(edges, edge)
	}
	return Subgraph{CenterEntityUID: center.EntityUID, Vertices: vertices, Edges: edges,
		Meta: GraphMeta{ContractVersion: GraphDTOContractVersion, SchemaVersion: GraphSchemaVersion,
			GeneratedAt: nowRFC3339(), WarningCodes: []string{}}}, nil
}

func (r *HugeGraphRepository) ShortestPath(ctx context.Context, scope GraphScope, query PathQuery) (Subgraph, error) {
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
	labels := normalizedRelationLabels(query.RelationTypes)
	var raw map[string]interface{}
	var err error
	if len(labels) <= 1 {
		raw, err = r.client.ShortestPath(ctx, query.SourceUID, query.TargetUID, query.MaxDepth, labels)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				return Subgraph{}, graphError(ErrGraphEmpty, "no path")
			}
			return Subgraph{}, graphError(ErrGraphUnavailable, err.Error())
		}
	} else {
		// HugeGraph's basic shortest-path endpoint accepts one label per
		// request. Evaluate each whitelisted relation independently and retain
		// the shortest bounded result instead of silently broadening the query.
		bestDepth := int(^uint(0) >> 1)
		for _, label := range labels {
			candidate, candidateErr := r.client.ShortestPath(ctx, query.SourceUID, query.TargetUID, query.MaxDepth, []string{label})
			if candidateErr != nil {
				if strings.Contains(candidateErr.Error(), "HTTP 404") {
					continue
				}
				return Subgraph{}, graphError(ErrGraphUnavailable, candidateErr.Error())
			}
			if depth := len(traverserPathIDs(candidate)); depth > 0 && depth < bestDepth {
				bestDepth, raw = depth, candidate
			}
		}
		if raw == nil {
			return Subgraph{}, graphError(ErrGraphEmpty, "no path")
		}
	}
	if len(labels) > 0 {
		raw["_edge_labels"] = labels
	}
	result, err := r.subgraphFromTraverser(ctx, scope, query.SourceUID, raw, query.MaxVertices, query.MaxEdges)
	if err != nil {
		return Subgraph{}, err
	}
	if _, ok := findEntity(result.Vertices, query.TargetUID); !ok {
		return Subgraph{}, graphError(ErrGraphEmpty, "no path")
	}
	return result, nil
}

func (r *HugeGraphRepository) Impact(ctx context.Context, scope GraphScope, query ImpactQuery) (Subgraph, error) {
	return r.Neighbors(ctx, scope, NeighborQuery{
		CenterEntityUID: query.RootUID, MaxDepth: query.MaxDepth, MaxVertices: query.MaxVertices,
		MaxEdges: query.MaxEdges, RelationTypes: impactRelationTypes(),
	})
}

func (r *HugeGraphRepository) CandidateSubgraph(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	if len(query.RelationTypes) == 0 {
		query.RelationTypes = candidateRelationTypes()
	}
	return r.Neighbors(ctx, scope, query)
}

func (r *HugeGraphRepository) BatchMutate(ctx context.Context, batch MutationBatch) (MutationResult, error) {
	result := MutationResult{Accepted: len(batch.Vertices) + len(batch.Edges)}
	if result.Accepted > 500 {
		return result, graphError(ErrGraphQueryLimitExceeded, "graph mutation batch exceeds 500 mutations")
	}
	if err := validateMutationBatch(batch); err != nil {
		return result, err
	}
	if r == nil || r.client == nil {
		return result, graphError(ErrGraphUnavailable, "HugeGraph client is not configured")
	}
	if len(batch.Vertices) > 0 {
		if err := r.client.PutVerticesBatch(ctx, batch.Vertices); err != nil {
			return result, graphError(ErrGraphUnavailable, err.Error())
		}
	}
	if len(batch.Edges) > 0 {
		if err := r.client.PutEdgesBatch(ctx, batch.Edges); err != nil {
			return result, graphError(ErrGraphUnavailable, err.Error())
		}
	}
	result.Applied = result.Accepted
	return result, nil
}

func (r *HugeGraphRepository) Health(ctx context.Context) GraphHealth {
	if r == nil || r.client == nil {
		return GraphHealth{Ready: false, Backend: "hugegraph", SchemaVersion: GraphSchemaVersion, ErrorCode: ErrGraphUnavailable}
	}
	if _, err := r.client.GetVertex(ctx, "__health_probe__"); err != nil && !strings.Contains(err.Error(), "HTTP 404") {
		return GraphHealth{Ready: false, Backend: "hugegraph", SchemaVersion: GraphSchemaVersion, ErrorCode: ErrGraphUnavailable}
	}
	return GraphHealth{Ready: true, Backend: "hugegraph", SchemaVersion: GraphSchemaVersion}
}

func (r *HugeGraphRepository) RawQuery(ctx context.Context, query string) (map[string]interface{}, error) {
	if r == nil || r.client == nil {
		return nil, graphError(ErrGraphUnavailable, "HugeGraph client is not configured")
	}
	return r.client.RawQuery(ctx, query)
}

func (r *HugeGraphRepository) DeleteEntity(ctx context.Context, scope GraphScope, uid string) error {
	if _, err := r.GetEntity(ctx, scope, uid); err != nil {
		return err
	}
	if err := r.client.DeleteVertex(ctx, uid); err != nil {
		return graphError(ErrGraphUnavailable, err.Error())
	}
	return nil
}

func (r *HugeGraphRepository) DeleteEdge(ctx context.Context, scope GraphScope, uid string) error {
	raw, err := r.client.GetEdge(ctx, uid)
	if err != nil {
		return graphError(ErrGraphUnavailable, err.Error())
	}
	edge, err := edgeFromHugeGraph(raw)
	if err != nil {
		return err
	}
	if edge.TenantID != scope.TenantID || !scope.Allows(Entity{TenantID: edge.TenantID, ClusterID: edge.ClusterID}) {
		return graphError(ErrGraphScopeViolation, uid)
	}
	if err := r.client.DeleteEdge(ctx, uid); err != nil {
		return graphError(ErrGraphUnavailable, err.Error())
	}
	return nil
}

func validateMutationBatch(batch MutationBatch) error {
	for _, entity := range batch.Vertices {
		if strings.TrimSpace(entity.EntityUID) == "" {
			return graphError(ErrGraphVersionConflict, "vertex entity_uid is required")
		}
		if err := ValidateEntityType(entity.EntityType); err != nil {
			return err
		}
		if batch.TenantID != "" && entity.TenantID != batch.TenantID {
			return graphError(ErrGraphScopeViolation, entity.EntityUID)
		}
		if batch.Source != "" && entity.Source != batch.Source {
			return graphError(ErrGraphVersionConflict, "vertex source mismatch")
		}
		if batch.Generation != 0 && entity.Generation != batch.Generation {
			return graphError(ErrGraphVersionConflict, "vertex generation mismatch")
		}
	}
	for _, edge := range batch.Edges {
		if strings.TrimSpace(edge.EdgeUID) == "" || strings.TrimSpace(edge.SourceUID) == "" || strings.TrimSpace(edge.TargetUID) == "" {
			return graphError(ErrGraphVersionConflict, "edge identity is required")
		}
		if err := ValidateRelation(edge.RelationType, inferMutationEntityType(batch.Vertices, edge.SourceUID), inferMutationEntityType(batch.Vertices, edge.TargetUID)); err != nil {
			// An edge may refer to an already projected vertex, so relation-pair
			// validation is repeated by the backend when both endpoint facts are
			// available. Unknown endpoint types are rejected here.
			if !strings.Contains(err.Error(), ErrUnknownEntityType) {
				return err
			}
		}
		if batch.TenantID != "" && edge.TenantID != batch.TenantID {
			return graphError(ErrGraphScopeViolation, edge.EdgeUID)
		}
		if batch.Source != "" && edge.Source != batch.Source {
			return graphError(ErrGraphVersionConflict, "edge source mismatch")
		}
		if batch.Generation != 0 && edge.Generation != batch.Generation {
			return graphError(ErrGraphVersionConflict, "edge generation mismatch")
		}
	}
	return nil
}

func inferMutationEntityType(vertices []Entity, uid string) string {
	for _, entity := range vertices {
		if entity.EntityUID == uid {
			return entity.EntityType
		}
	}
	return ""
}

func entityFromHugeGraph(raw map[string]interface{}, fallbackUID string) (Entity, error) {
	properties := mapValue(raw, "properties")
	if len(properties) == 0 {
		properties = raw
	}
	uid := stringValue(properties, "entity_uid")
	if uid == "" {
		uid = stringValue(raw, "id")
	}
	if uid == "" {
		uid = fallbackUID
	}
	entity := Entity{
		EntityUID: uid, EntityType: stringValue(properties, "entity_type"), TenantID: stringValue(properties, "tenant_id"),
		ClusterID: stringValue(properties, "cluster_id"), Namespace: stringValue(properties, "namespace"), Name: stringValue(properties, "name"),
		NameKey: stringValue(properties, "name_key"), Source: stringValue(properties, "source"), SourceUID: stringValue(properties, "source_uid"),
		Status: stringValue(properties, "status"), Health: stringValue(properties, "health"), Resolution: stringValue(properties, "resolution"),
		Confidence: floatValue(properties, "confidence"), FirstSeenMS: intValue(properties, "first_seen_ms"), LastSeenMS: intValue(properties, "last_seen_ms"),
		Generation: intValue(properties, "generation"), AttrsVersion: intValue(properties, "attrs_version"), Attrs: attrsValue(properties["attrs_json"]),
	}
	if err := ValidateEntityType(entity.EntityType); err != nil {
		return Entity{}, err
	}
	if entity.NameKey == "" {
		entity.NameKey = NameKeyV1(entity.Name)
	}
	return entity, nil
}

func edgeFromHugeGraph(raw map[string]interface{}) (Edge, error) {
	properties := mapValue(raw, "properties")
	if len(properties) == 0 {
		properties = raw
	}
	edge := Edge{
		EdgeUID: stringValue(properties, "edge_uid"), SourceUID: firstString(raw, "outV", "source", "source_uid"), TargetUID: firstString(raw, "inV", "target", "target_uid"),
		RelationType: firstString(raw, "label", "relation_type"), TenantID: stringValue(properties, "tenant_id"), ClusterID: stringValue(properties, "cluster_id"),
		Status: stringValue(properties, "status"), Source: stringValue(properties, "source"), Confidence: floatValue(properties, "confidence"),
		Generation: intValue(properties, "generation"), FirstSeenMS: intValue(properties, "first_seen_ms"), LastSeenMS: intValue(properties, "last_seen_ms"),
		ValidFromMS: intValue(properties, "valid_from_ms"), ValidToMS: intValue(properties, "valid_to_ms"), PropagatesFailure: boolValue(properties, "propagates_failure"),
		CandidateDirection: stringValue(properties, "candidate_direction"), ImpactDirection: stringValue(properties, "impact_direction"), AttrsVersion: intValue(properties, "attrs_version"), Attrs: attrsValue(properties["attrs_json"]),
	}
	if edge.EdgeUID == "" {
		edge.EdgeUID = stringValue(raw, "id")
	}
	if edge.EdgeUID == "" && edge.TenantID != "" && edge.RelationType != "" && edge.SourceUID != "" && edge.TargetUID != "" {
		edge.EdgeUID = EdgeUID(edge.TenantID, edge.RelationType, edge.SourceUID, edge.TargetUID)
	}
	if err := ValidateEntityType("service"); err != nil { // keep validation package-linked in generated adapters
		return Edge{}, err
	}
	return edge, nil
}

func (r *HugeGraphRepository) subgraphFromTraverser(ctx context.Context, scope GraphScope, center string, raw map[string]interface{}, maxVertices, maxEdges int) (Subgraph, error) {
	vertices, edges := []Entity{}, []Edge{}
	vertexIDs := map[string]struct{}{}
	pathIDs := traverserPathIDs(raw)
	for _, uid := range pathIDs {
		if uid != "" {
			vertexIDs[uid] = struct{}{}
		}
	}
	for _, item := range interfaceSlice(raw["vertices"]) {
		if vertex, ok := item.(map[string]interface{}); ok {
			entity, err := entityFromHugeGraph(vertex, "")
			if err != nil {
				return Subgraph{}, err
			}
			vertexIDs[entity.EntityUID] = struct{}{}
			vertices = append(vertices, entity)
			continue
		}
		if uid := fmt.Sprint(item); uid != "" {
			vertexIDs[uid] = struct{}{}
		}
	}
	if _, ok := vertexIDs[center]; !ok {
		vertexIDs[center] = struct{}{}
	}
	if len(vertices) < len(vertexIDs) {
		ids := make([]string, 0, len(vertexIDs))
		for uid := range vertexIDs {
			if _, ok := findEntity(vertices, uid); !ok {
				ids = append(ids, uid)
			}
		}
		sort.Strings(ids)
		for _, uid := range ids {
			entity, err := r.GetEntity(ctx, scope, uid)
			if err != nil {
				return Subgraph{}, err
			}
			vertices = append(vertices, entity)
		}
	}
	filtered := vertices[:0]
	for _, entity := range vertices {
		if !scope.Allows(entity) {
			return Subgraph{}, graphError(ErrGraphScopeViolation, entity.EntityUID)
		}
		filtered = append(filtered, entity)
	}
	vertices = filtered
	for _, item := range interfaceSlice(raw["edges"]) {
		if edgeMap, ok := item.(map[string]interface{}); ok {
			edge, err := edgeFromHugeGraph(edgeMap)
			if err != nil {
				return Subgraph{}, err
			}
			if source, ok := findEntity(vertices, edge.SourceUID); !ok || !scope.Allows(source) {
				continue
			} else if target, ok := findEntity(vertices, edge.TargetUID); !ok || !scope.Allows(target) {
				continue
			} else if edge.TenantID != "" && edge.TenantID != scope.TenantID {
				continue
			}
			edges = append(edges, edge)
		}
	}
	if len(edges) == 0 && len(pathIDs) > 1 {
		labels := stringSlice(raw["_edge_labels"])
		for index := 0; index < len(pathIDs)-1 && len(edges) < maxEdges; index++ {
			between, err := r.client.EdgesBetween(ctx, pathIDs[index], pathIDs[index+1], labels)
			if err != nil {
				return Subgraph{}, graphError(ErrGraphUnavailable, err.Error())
			}
			for _, rawEdge := range between {
				edge, err := edgeFromHugeGraph(rawEdge)
				if err != nil {
					return Subgraph{}, err
				}
				if source, ok := findEntity(vertices, edge.SourceUID); !ok || !scope.Allows(source) {
					continue
				} else if target, ok := findEntity(vertices, edge.TargetUID); !ok || !scope.Allows(target) {
					continue
				}
				edges = append(edges, edge)
				if len(edges) >= maxEdges {
					break
				}
			}
		}
	}
	if len(vertices) > maxVertices {
		vertices = vertices[:maxVertices]
	}
	if len(edges) > maxEdges {
		edges = edges[:maxEdges]
	}
	return Subgraph{CenterEntityUID: center, Vertices: vertices, Edges: edges, Meta: GraphMeta{ContractVersion: GraphDTOContractVersion, SchemaVersion: GraphSchemaVersion, GeneratedAt: nowRFC3339(), WarningCodes: []string{}}}, nil
}

func stringSlice(value interface{}) []string {
	raw, ok := value.([]string)
	if ok {
		return raw
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func traverserPathIDs(raw map[string]interface{}) []string {
	path, ok := raw["path"].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(path))
	for _, item := range path {
		switch value := item.(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				result = append(result, value)
			}
		case map[string]interface{}:
			if uid := firstString(value, "id", "entity_uid"); uid != "" {
				result = append(result, uid)
			}
		}
	}
	return result
}

func nowRFC3339() string { return time.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00") }

func mapValue(raw map[string]interface{}, key string) map[string]interface{} {
	value, ok := raw[key].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return value
}

func stringValue(raw map[string]interface{}, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func intValue(raw map[string]interface{}, key string) int64 {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0
	}
	switch n := value.(type) {
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	case int64:
		return n
	case int:
		return int64(n)
	default:
		v, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return v
	}
}

func floatValue(raw map[string]interface{}, key string) float64 {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0
	}
	switch n := value.(type) {
	case float64:
		return n
	case json.Number:
		v, _ := n.Float64()
		return v
	default:
		v, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
		return v
	}
}

func boolValue(raw map[string]interface{}, key string) bool {
	value, ok := raw[key]
	if !ok || value == nil {
		return false
	}
	if b, ok := value.(bool); ok {
		return b
	}
	b, _ := strconv.ParseBool(fmt.Sprint(value))
	return b
}

func attrsValue(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if attrs, ok := value.(map[string]interface{}); ok {
		return attrs
	}
	var attrs map[string]interface{}
	if json.Unmarshal([]byte(fmt.Sprint(value)), &attrs) == nil {
		return attrs
	}
	return nil
}

func interfaceSlice(value interface{}) []interface{} {
	switch values := value.(type) {
	case []interface{}:
		return values
	case nil:
		return nil
	default:
		return []interface{}{value}
	}
}

func findEntity(items []Entity, uid string) (Entity, bool) {
	for _, entity := range items {
		if entity.EntityUID == uid {
			return entity, true
		}
	}
	return Entity{}, false
}

func normalizedRelationLabels(relations []string) []string {
	labels := append([]string(nil), relations...)
	for i := range labels {
		labels[i] = strings.ToUpper(strings.TrimSpace(labels[i]))
	}
	sort.Strings(labels)
	return labels
}

func normalizedDirection(direction string) string {
	direction = strings.ToUpper(strings.TrimSpace(direction))
	if direction == "OUT" || direction == "IN" {
		return direction
	}
	return "BOTH"
}

func candidateRelationTypes() []string {
	return []string{"REPRESENTS", "BACKED_BY", "RUNS_ON", "HOSTS", "HAS_COMPONENT", "DEPENDS_ON", "USES_VOLUME", "BOUND_TO", "ATTACHED_TO", "INSTANCE_OF"}
}

func impactRelationTypes() []string {
	return []string{"REPRESENTS", "BACKED_BY", "RUNS_ON", "HOSTS", "HAS_COMPONENT", "DEPENDS_ON", "USES_VOLUME", "BOUND_TO", "ATTACHED_TO", "CONNECTS_TO"}
}

var _ GraphRepository = (*HugeGraphRepository)(nil)
