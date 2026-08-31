package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// LegacyMySQLRepository is the compatibility read/write adapter for the old
// topology tables. It is deliberately isolated here so new graph builders do
// not grow another dependency on topology_nodes/topology_relations.
type LegacyMySQLRepository struct {
	nodes *store.TopologyNodeDAO
	edges *store.TopologyRelationDAO
}

func NewLegacyMySQLRepository() *LegacyMySQLRepository {
	return &LegacyMySQLRepository{nodes: &store.TopologyNodeDAO{}, edges: &store.TopologyRelationDAO{}}
}

func (r *LegacyMySQLRepository) GetEntity(ctx context.Context, scope GraphScope, uid string) (Entity, error) {
	if err := ctx.Err(); err != nil {
		return Entity{}, err
	}
	id, typ, ok := legacyUIDParts(uid)
	if !ok {
		return Entity{}, graphError(ErrGraphEntityNotFound, uid)
	}
	node, err := r.nodes.Get(id)
	if err != nil {
		return Entity{}, graphError(ErrGraphUnavailable, err.Error())
	}
	if node == nil || (typ != "" && legacyEntityType(node.Type) != typ) {
		return Entity{}, graphError(ErrGraphEntityNotFound, uid)
	}
	entity := legacyEntity(node)
	if !scope.Allows(entity) {
		return Entity{}, graphError(ErrGraphScopeViolation, uid)
	}
	return entity, nil
}

func (r *LegacyMySQLRepository) SearchEntities(ctx context.Context, scope GraphScope, query EntitySearchQuery) ([]Entity, error) {
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
	nodes, _, err := r.nodes.List(query.EntityType, query.Name, limit, 0)
	if err != nil {
		return nil, graphError(ErrGraphUnavailable, err.Error())
	}
	result := make([]Entity, 0, len(nodes))
	for _, node := range nodes {
		entity := legacyEntity(&node)
		if scope.Allows(entity) {
			result = append(result, entity)
		}
	}
	return result, nil
}

func (r *LegacyMySQLRepository) Neighbors(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	memory, err := r.snapshot(ctx, scope)
	if err != nil {
		return Subgraph{}, err
	}
	return memory.Neighbors(ctx, scope, query)
}

func (r *LegacyMySQLRepository) ShortestPath(ctx context.Context, scope GraphScope, query PathQuery) (Subgraph, error) {
	memory, err := r.snapshot(ctx, scope)
	if err != nil {
		return Subgraph{}, err
	}
	return memory.ShortestPath(ctx, scope, query)
}

func (r *LegacyMySQLRepository) Impact(ctx context.Context, scope GraphScope, query ImpactQuery) (Subgraph, error) {
	memory, err := r.snapshot(ctx, scope)
	if err != nil {
		return Subgraph{}, err
	}
	return memory.Impact(ctx, scope, query)
}

func (r *LegacyMySQLRepository) CandidateSubgraph(ctx context.Context, scope GraphScope, query NeighborQuery) (Subgraph, error) {
	memory, err := r.snapshot(ctx, scope)
	if err != nil {
		return Subgraph{}, err
	}
	return memory.CandidateSubgraph(ctx, scope, query)
}

func (r *LegacyMySQLRepository) BatchMutate(ctx context.Context, batch MutationBatch) (MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if len(batch.Vertices)+len(batch.Edges) > 500 {
		return MutationResult{}, graphError(ErrGraphQueryLimitExceeded, "graph mutation batch exceeds 500 mutations")
	}
	result := MutationResult{Accepted: len(batch.Vertices) + len(batch.Edges)}
	ids := map[string]int64{}
	for _, entity := range batch.Vertices {
		if err := ValidateEntityType(entity.EntityType); err != nil {
			return result, err
		}
		if batch.TenantID != "" && entity.TenantID != batch.TenantID {
			return result, graphError(ErrGraphScopeViolation, entity.EntityUID)
		}
		props := cloneJSONMap(entity.Attrs)
		props["tenant_id"] = entity.TenantID
		props["cluster_id"] = entity.ClusterID
		props["entity_uid"] = entity.EntityUID
		encoded, err := json.Marshal(props)
		if err != nil {
			return result, err
		}
		typ := legacyNodeType(entity.EntityType)
		name := entity.Name
		// Legacy records cannot carry the new UID as a primary key. Keep the UID
		// in props so a later reconcile can round-trip the canonical identity.
		id, err := r.nodes.Create(&store.TopologyNode{Type: typ, Name: name, PropsJSON: string(encoded)})
		if err != nil {
			return result, graphError(ErrGraphUnavailable, err.Error())
		}
		ids[entity.EntityUID] = id
		result.Applied++
	}
	for _, edge := range batch.Edges {
		sourceID, sourceOK := ids[edge.SourceUID]
		targetID, targetOK := ids[edge.TargetUID]
		if !sourceOK || !targetOK {
			return result, graphError(ErrGraphFeatureUnavailable, "legacy edge mutation requires endpoints in the same batch")
		}
		props := cloneJSONMap(edge.Attrs)
		props["tenant_id"] = edge.TenantID
		props["cluster_id"] = edge.ClusterID
		props["edge_uid"] = edge.EdgeUID
		encoded, err := json.Marshal(props)
		if err != nil {
			return result, err
		}
		if _, err := r.edges.Create(&store.TopologyRelation{SrcID: sourceID, DstID: targetID, Type: strings.ToLower(edge.RelationType), PropsJSON: string(encoded)}); err != nil {
			return result, graphError(ErrGraphUnavailable, err.Error())
		}
		result.Applied++
	}
	return result, nil
}

func (r *LegacyMySQLRepository) Health(context.Context) GraphHealth {
	if store.GetDB() == nil {
		return GraphHealth{Ready: false, Backend: "legacy_mysql", SchemaVersion: GraphSchemaVersion, ErrorCode: ErrGraphUnavailable}
	}
	return GraphHealth{Ready: true, Backend: "legacy_mysql", SchemaVersion: GraphSchemaVersion}
}

func (r *LegacyMySQLRepository) snapshot(ctx context.Context, scope GraphScope) (*MemoryRepository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes, _, err := r.nodes.List("", "", DefaultInternalMaxVertices, 0)
	if err != nil {
		return nil, graphError(ErrGraphUnavailable, err.Error())
	}
	memory := NewMemoryRepository()
	entities := make([]Entity, 0, len(nodes))
	legacyIDs := map[int64]string{}
	for _, node := range nodes {
		entity := legacyEntity(&node)
		if scope.Allows(entity) {
			entities = append(entities, entity)
			legacyIDs[node.ID] = entity.EntityUID
		}
	}
	// The old DAO has no endpoint-only filter, so load only the bounded legacy
	// read window and discard edges whose endpoint leaves the authenticated scope.
	edges, _, err := r.edges.List(0, 0, "", DefaultInternalMaxEdges, 0)
	if err != nil {
		return nil, graphError(ErrGraphUnavailable, err.Error())
	}
	graphEdges := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		source, sourceOK := legacyIDs[edge.SrcID]
		target, targetOK := legacyIDs[edge.DstID]
		if !sourceOK || !targetOK {
			continue
		}
		props := parseLegacyProps(edge.PropsJSON)
		graphEdges = append(graphEdges, Edge{EdgeUID: firstNonEmptyString(stringMapValue(props, "edge_uid"), EdgeUID(stringMapValue(props, "tenant_id"), strings.ToUpper(edge.Type), source, target)), SourceUID: source, TargetUID: target, RelationType: strings.ToUpper(edge.Type), TenantID: stringMapValue(props, "tenant_id"), ClusterID: stringMapValue(props, "cluster_id"), Status: "active", Source: "legacy_mysql", Attrs: props})
	}
	if _, err := memory.BatchMutate(ctx, MutationBatch{Vertices: entities, Edges: graphEdges}); err != nil {
		return nil, err
	}
	return memory, nil
}

func legacyEntity(node *store.TopologyNode) Entity {
	props := parseLegacyProps(node.PropsJSON)
	typ := legacyEntityType(node.Type)
	tenant := stringMapValue(props, "tenant_id")
	cluster := stringMapValue(props, "cluster_id")
	uid := firstNonEmptyString(stringMapValue(props, "entity_uid"), EntityUID("legacy-"+typ, strconv.FormatInt(node.ID, 10)))
	return Entity{EntityUID: uid, EntityType: typ, TenantID: tenant, ClusterID: cluster, Name: node.Name, NameKey: NameKeyV1(node.Name), Source: "legacy_mysql", Status: firstNonEmptyString(stringMapValue(props, "status"), "active"), Attrs: props}
}

func legacyUIDParts(uid string) (int64, string, bool) {
	parts := strings.Split(uid, ":")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "legacy-") || parts[1] != "v1" {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	return id, strings.TrimPrefix(parts[0], "legacy-"), err == nil && id > 0
}

func legacyEntityType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "app":
		return "application"
	case "cluster":
		return "k8s_cluster"
	case "device":
		return "physical_server"
	case "rack":
		return "network"
	default:
		if err := ValidateEntityType(raw); err == nil {
			return strings.ToLower(strings.TrimSpace(raw))
		}
		return "service"
	}
}

func legacyNodeType(entityType string) string {
	switch entityType {
	case "application":
		return "app"
	case "k8s_cluster":
		return "cluster"
	case "physical_server":
		return "device"
	case "network":
		return "rack"
	default:
		return entityType
	}
}

func parseLegacyProps(raw string) map[string]interface{} {
	var props map[string]interface{}
	if json.Unmarshal([]byte(raw), &props) != nil || props == nil {
		return map[string]interface{}{}
	}
	return props
}

func stringMapValue(values map[string]interface{}, key string) string {
	if value, ok := values[key]; ok && value != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneJSONMap(values map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(values)+4)
	for key, value := range values {
		result[key] = value
	}
	return result
}

var _ GraphRepository = (*LegacyMySQLRepository)(nil)
