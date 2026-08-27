package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	knowledgeGraphReadCapability  = "control_plane.knowledge_graph.read"
	knowledgeGraphWriteCapability = "control_plane.knowledge_graph.write"
)

type knowledgeGraphRequest struct {
	Operation      string                   `json:"operation"`
	ClusterID      string                   `json:"cluster_id"`
	Type           string                   `json:"type"`
	Name           string                   `json:"name"`
	Props          map[string]interface{}   `json:"props"`
	SrcID          int64                    `json:"src_id"`
	DstID          int64                    `json:"dst_id"`
	EdgeType       string                   `json:"edge_type"`
	EdgeProps      map[string]interface{}   `json:"edge_props"`
	NodeID         int64                    `json:"node_id"`
	Hops           int                      `json:"hops"`
	Depth          int                      `json:"depth"`
	EdgeTypes      []string                 `json:"edge_types"`
	FromType       string                   `json:"from_type"`
	FromName       string                   `json:"from_name"`
	ToType         string                   `json:"to_type"`
	ToName         string                   `json:"to_name"`
	Operations     []knowledgeGraphMutation `json:"operations"`
	EntityUID      string                   `json:"entity_uid"`
	Generation     int64                    `json:"generation"`
	Source         string                   `json:"source"`
	Mutations      []graphMutationRequest   `json:"mutations"`
	Phase          string                   `json:"phase"`
	ReconcileRunID string                   `json:"reconcile_run_id"`
	LeaseKey       string                   `json:"lease_key"`
	LeaseOwnerID   string                   `json:"lease_owner_id"`
	LeaseEpoch     int64                    `json:"lease_epoch"`
	LeaseToken     string                   `json:"lease_token"`
	Watermark      string                   `json:"watermark"`
	Error          string                   `json:"error"`
	VerticesSeen   int64                    `json:"vertices_seen"`
	EdgesSeen      int64                    `json:"edges_seen"`
	VerticesStaled int64                    `json:"vertices_staled"`
	EdgesStaled    int64                    `json:"edges_staled"`
}

type graphMutationRequest struct {
	MutationID string          `json:"mutation_id"`
	Kind       string          `json:"kind"`
	Vertex     graphpkg.Entity `json:"vertex"`
	Edge       graphpkg.Edge   `json:"edge"`
}

type knowledgeGraphMutation struct {
	Operation string                 `json:"operation"`
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	Props     map[string]interface{} `json:"props"`
	SrcID     int64                  `json:"src_id"`
	DstID     int64                  `json:"dst_id"`
	EdgeType  string                 `json:"edge_type"`
	EdgeProps map[string]interface{} `json:"edge_props"`
}

// InternalControlPlaneKnowledgeGraph is the persistence boundary for the
// orchestrator knowledge graph. The orchestrator sends typed mutations and
// receives graph snapshots; only query-api touches topology tables.
func (h *Handler) InternalControlPlaneKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	var req knowledgeGraphRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if req.Operation == "" || (req.Operation != "snapshot" && req.Operation != "find_node" && req.Operation != "upsert_node" && req.Operation != "upsert_edge" && req.Operation != "batch_upsert" && req.Operation != "reconcile" && req.Operation != "get_vertex" && req.Operation != "batch_mutate" && req.Operation != "mark_stale_generation" && req.Operation != "reconcile_scope" && req.Operation != "health") {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	capability := knowledgeGraphReadCapability
	if req.Operation == "upsert_node" || req.Operation == "upsert_edge" || req.Operation == "batch_upsert" || req.Operation == "reconcile" || req.Operation == "batch_mutate" || req.Operation == "mark_stale_generation" || req.Operation == "reconcile_scope" {
		capability = knowledgeGraphWriteCapability
	}
	rctx, authErr := authorizeInternalControlPlane(r, capability, "ai-orchestrator")
	if authErr != nil {
		respondInternalQueryError(w, authErr)
		return
	}
	// When the caller signs a cluster-scoped context, the request body cannot
	// widen it or switch to another cluster. Read-all snapshots are allowed only
	// for the legacy graph-navigation helper, which uses run scope.
	if rctx.ClusterID != "" && req.ClusterID != "" && rctx.ClusterID != req.ClusterID {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeContextScopeMismatch})
		return
	}
	if req.ClusterID == "" && rctx.ClusterID != "" {
		req.ClusterID = rctx.ClusterID
	}
	if rctx.ClusterID != "" {
		if req.Operation == "upsert_node" && knowledgeGraphCluster(req.Props) != rctx.ClusterID {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeContextScopeMismatch})
			return
		}
		if req.Operation == "upsert_edge" && knowledgeGraphCluster(req.EdgeProps) != rctx.ClusterID {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeContextScopeMismatch})
			return
		}
	}

	var (
		result map[string]interface{}
		err    error
	)
	if req.Operation == "get_vertex" || req.Operation == "batch_mutate" || req.Operation == "mark_stale_generation" || req.Operation == "reconcile_scope" || req.Operation == "health" {
		result, err = h.internalGraphControlOperation(req, rctx)
		if err != nil {
			respondGraphErrorFromGo(w, err)
			return
		}
		respondJSON(w, http.StatusOK, result)
		return
	}
	switch req.Operation {
	case "snapshot":
		result, err = knowledgeGraphSnapshot(req.ClusterID)
	case "find_node":
		result, err = knowledgeGraphFindNode(req.Type, req.Name, req.ClusterID)
	case "upsert_node":
		result, err = knowledgeGraphUpsertNode(req.Type, req.Name, req.Props)
	case "upsert_edge":
		result, err = knowledgeGraphUpsertEdge(req.SrcID, req.DstID, req.EdgeType, req.EdgeProps)
	case "batch_upsert":
		result, err = knowledgeGraphBatchUpsert(req.Operations)
	case "reconcile":
		result, err = knowledgeGraphReconcile()
	}
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "knowledge_graph_unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) internalGraphControlOperation(req knowledgeGraphRequest, rctx *internalQueryCtx) (map[string]interface{}, error) {
	if h.graphRepo == nil || h.graphInitErr != nil {
		return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, "knowledge graph is not configured")
	}
	scope := graphpkg.GraphScope{TenantID: rctx.TenantID, ClusterIDs: map[string]struct{}{rctx.ClusterID: {}}}
	switch req.Operation {
	case "health":
		return map[string]interface{}{"health": h.graphRepo.Health(context.Background())}, nil
	case "get_vertex":
		if req.EntityUID == "" {
			return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "entity_uid is required")
		}
		entity, err := h.graphRepo.GetEntity(context.Background(), scope, req.EntityUID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"entity": entity}, nil
	case "batch_mutate":
		if len(req.Mutations) == 0 || len(req.Mutations) > 500 {
			return nil, graphpkg.NewError(graphpkg.ErrGraphQueryLimitExceeded, "mutations must contain 1 to 500 items")
		}
		if err := h.verifyGraphReconcileLease(req); err != nil {
			return nil, err
		}
		batch := graphpkg.MutationBatch{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID, Source: req.Source, Generation: req.Generation}
		for _, mutation := range req.Mutations {
			switch mutation.Kind {
			case "upsert_vertex":
				batch.Vertices = append(batch.Vertices, mutation.Vertex)
			case "upsert_edge":
				batch.Edges = append(batch.Edges, mutation.Edge)
			default:
				return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "unsupported mutation kind")
			}
		}
		result, err := h.graphRepo.BatchMutate(context.Background(), batch)
		if err != nil {
			return nil, err
		}
		if h.graphAliasDAO != nil {
			for _, entity := range batch.Vertices {
				_ = h.graphAliasDAO.Upsert(store.GraphEntityAlias{TenantID: entity.TenantID, ScopeClusterID: entity.ClusterID, Source: entity.Source, AliasType: "name", AliasValue: entity.NameKey, CanonicalEntityUID: entity.EntityUID})
			}
		}
		return map[string]interface{}{"result": result}, nil
	case "reconcile_scope":
		return h.reconcileGraphScope(req, rctx)
	case "mark_stale_generation":
		if err := h.verifyGraphReconcileLease(req); err != nil {
			return nil, err
		}
		if req.Source == "" || req.Generation <= 0 {
			return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "source and positive generation are required")
		}
		marker, ok := h.graphRepo.(graphpkg.GenerationStaleMarker)
		if !ok {
			return nil, graphpkg.NewError(graphpkg.ErrGraphFeatureUnavailable, "generation stale marker is not configured")
		}
		vertices, edges, err := marker.MarkStaleByGeneration(context.Background(), req.Source, rctx.TenantID, req.ClusterID, req.Generation)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"marked_vertices": vertices, "marked_edges": edges}, nil
	default:
		return nil, graphpkg.NewError(graphpkg.ErrGraphFeatureUnavailable, req.Operation+" is not available in this backend")
	}
}

var graphReconcileSources = map[string]struct{}{
	"catalog": {}, "hardware": {}, "kubernetes": {}, "kubevirt": {},
	"middleware": {}, "trace": {}, "change": {}, "network": {},
}

func graphReconcileLeaseTTL() time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GRAPH_RECONCILE_LEASE_TTL_SECONDS")))
	if err != nil || seconds < 30 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func graphUUID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1000000000000)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16])
}

func (h *Handler) verifyGraphReconcileLease(req knowledgeGraphRequest) error {
	if req.LeaseKey == "" || req.LeaseOwnerID == "" || req.LeaseToken == "" || req.LeaseEpoch <= 0 {
		return graphpkg.NewError("GRAPH_LEASE_FENCED", "active graph reconcile lease is required")
	}
	ok, err := (&store.GraphWorkerLeaseDAO{}).Verify(req.LeaseKey, req.LeaseOwnerID, req.LeaseToken, req.LeaseEpoch)
	if err != nil {
		return graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
	}
	if !ok {
		return graphpkg.NewError("GRAPH_LEASE_FENCED", "graph reconcile lease is expired or replaced")
	}
	return nil
}

func (h *Handler) reconcileGraphScope(req knowledgeGraphRequest, rctx *internalQueryCtx) (map[string]interface{}, error) {
	if req.Source == "" {
		return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "source is required")
	}
	if _, ok := graphReconcileSources[req.Source]; !ok {
		return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "unsupported reconcile source")
	}
	if req.ClusterID == "" {
		return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "cluster_id is required")
	}
	leases := &store.GraphWorkerLeaseDAO{}
	if req.Phase == "start" {
		leaseKey := fmt.Sprintf("graph-reconcile:%s:%s:%s", req.Source, rctx.TenantID, req.ClusterID)
		ownerID := "ai-orchestrator-" + graphUUID()
		lease, acquired, err := leases.Acquire(leaseKey, ownerID, graphReconcileLeaseTTL())
		if err != nil {
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		if !acquired {
			return map[string]interface{}{"acquired": false}, nil
		}
		runID := graphUUID()
		if err := (&store.GraphReconcileRunDAO{}).Start(store.GraphReconcileRun{
			ReconcileRunID: runID, Source: req.Source, TenantID: rctx.TenantID,
			ScopeClusterID: req.ClusterID, Generation: 0, Status: "running",
		}); err != nil {
			_ = leases.Release(lease.LeaseKey, lease.OwnerID, lease.TokenHash, lease.LeaseEpoch)
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		stateDAO := &store.GraphSyncStateDAO{}
		previousGeneration, generation, err := stateDAO.StartLocked(req.Source, rctx.TenantID, req.ClusterID)
		if err != nil {
			_ = (&store.GraphReconcileRunDAO{}).Finish(runID, "failed", err.Error(), 0, 0, 0, 0)
			_ = leases.Release(lease.LeaseKey, lease.OwnerID, lease.TokenHash, lease.LeaseEpoch)
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		// The run row is intentionally inserted before state.StartLocked.  Fill
		// the generation assigned by the locked state row in the audit row.
		if err := (&store.GraphReconcileRunDAO{}).SetGeneration(runID, generation); err != nil {
			_ = stateDAO.Finish(req.Source, rctx.TenantID, req.ClusterID, "", previousGeneration, "failed", err.Error())
			_ = (&store.GraphReconcileRunDAO{}).Finish(runID, "failed", err.Error(), 0, 0, 0, 0)
			_ = leases.Release(lease.LeaseKey, lease.OwnerID, lease.TokenHash, lease.LeaseEpoch)
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		return map[string]interface{}{
			"acquired": true, "reconcile_run_id": runID, "generation": generation,
			"lease_key": lease.LeaseKey, "lease_owner_id": lease.OwnerID,
			"lease_epoch": lease.LeaseEpoch, "lease_token": lease.TokenHash,
		}, nil
	}
	if req.ReconcileRunID == "" || req.Generation <= 0 {
		return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "reconcile run and generation are required")
	}
	if err := h.verifyGraphReconcileLease(req); err != nil {
		return nil, err
	}
	stateDAO := &store.GraphSyncStateDAO{}
	runDAO := &store.GraphReconcileRunDAO{}
	previousGeneration := req.Generation - 1
	if previousGeneration < 0 {
		previousGeneration = 0
	}
	status := req.Phase
	if status == "no_data" {
		// An empty successful source view is safe, but it is not a new graph
		// generation and must not trigger stale cleanup.
		if err := stateDAO.Finish(req.Source, rctx.TenantID, req.ClusterID, req.Watermark, previousGeneration, "success", ""); err != nil {
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		if err := runDAO.Finish(req.ReconcileRunID, "success", "no_data", 0, 0, 0, 0); err != nil {
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		_ = leases.Release(req.LeaseKey, req.LeaseOwnerID, req.LeaseToken, req.LeaseEpoch)
		return map[string]interface{}{"status": "no_data"}, nil
	}
	if status == "failed" {
		if err := stateDAO.Finish(req.Source, rctx.TenantID, req.ClusterID, req.Watermark, previousGeneration, "failed", req.Error); err != nil {
			return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
		}
		_ = runDAO.Finish(req.ReconcileRunID, "failed", req.Error, req.VerticesSeen, req.EdgesSeen, 0, 0)
		_ = leases.Release(req.LeaseKey, req.LeaseOwnerID, req.LeaseToken, req.LeaseEpoch)
		return map[string]interface{}{"status": "failed"}, nil
	}
	if status != "success" {
		return nil, graphpkg.NewError("GRAPH_INVALID_ARGUMENT", "unsupported reconcile phase")
	}
	if err := stateDAO.Finish(req.Source, rctx.TenantID, req.ClusterID, req.Watermark, req.Generation, "success", ""); err != nil {
		return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
	}
	if err := runDAO.Finish(req.ReconcileRunID, "success", "", req.VerticesSeen, req.EdgesSeen, req.VerticesStaled, req.EdgesStaled); err != nil {
		return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
	}
	if err := leases.Release(req.LeaseKey, req.LeaseOwnerID, req.LeaseToken, req.LeaseEpoch); err != nil {
		return nil, graphpkg.NewError(graphpkg.ErrGraphUnavailable, err.Error())
	}
	return map[string]interface{}{"status": "success"}, nil
}

func knowledgeGraphProps(raw string) map[string]interface{} {
	var props map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &props); err != nil || props == nil {
		return map[string]interface{}{}
	}
	return props
}

func knowledgeGraphCluster(props map[string]interface{}) string {
	if v, ok := props["cluster_id"]; ok && v != nil {
		value := strings.TrimSpace(fmt.Sprint(v))
		if value != "" {
			return value
		}
	}
	return "default"
}

func knowledgeGraphSameCluster(props map[string]interface{}, clusterID string) bool {
	if clusterID == "" {
		return true
	}
	return knowledgeGraphCluster(props) == clusterID
}

func knowledgeGraphJSON(value map[string]interface{}) (string, error) {
	if value == nil {
		value = map[string]interface{}{}
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func knowledgeGraphUpsertNode(typeName, name string, props map[string]interface{}) (map[string]interface{}, error) {
	if strings.TrimSpace(typeName) == "" || strings.TrimSpace(name) == "" {
		return nil, errors.New("type and name required")
	}
	if props == nil {
		props = map[string]interface{}{}
	}
	props = cloneMap(props)
	if _, ok := props["cluster_id"]; !ok {
		props["cluster_id"] = "default"
	}
	if _, ok := props["created_by"]; !ok {
		props["created_by"] = "auto"
	}
	dao := &store.TopologyNodeDAO{}
	items, _, err := dao.List(typeName, name, 1000, 0)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		old := knowledgeGraphProps(item.PropsJSON)
		if !knowledgeGraphSameCluster(old, knowledgeGraphCluster(props)) {
			continue
		}
		if old["created_by"] == "manual" {
			return map[string]interface{}{"id": item.ID, "created": false, "skipped": true}, nil
		}
		merged := cloneMap(old)
		for k, v := range props {
			merged[k] = v
		}
		encoded, err := knowledgeGraphJSON(merged)
		if err != nil {
			return nil, err
		}
		if err := dao.Update(item.ID, &store.TopologyNode{Type: typeName, Name: name, PropsJSON: encoded}); err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": item.ID, "created": false}, nil
	}
	encoded, err := knowledgeGraphJSON(props)
	if err != nil {
		return nil, err
	}
	id, err := dao.Create(&store.TopologyNode{Type: typeName, Name: name, PropsJSON: encoded})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": id, "created": true}, nil
}

func knowledgeGraphUpsertEdge(srcID, dstID int64, edgeType string, props map[string]interface{}) (map[string]interface{}, error) {
	if srcID <= 0 || dstID <= 0 || strings.TrimSpace(edgeType) == "" {
		return nil, errors.New("src_id, dst_id and edge_type required")
	}
	if props == nil {
		props = map[string]interface{}{}
	}
	props = cloneMap(props)
	if _, ok := props["cluster_id"]; !ok {
		props["cluster_id"] = "default"
	}
	if _, ok := props["created_by"]; !ok {
		props["created_by"] = "auto"
	}
	dao := &store.TopologyRelationDAO{}
	items, _, err := dao.List(srcID, dstID, edgeType, 1000, 0)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		old := knowledgeGraphProps(item.PropsJSON)
		if !knowledgeGraphSameCluster(old, knowledgeGraphCluster(props)) {
			continue
		}
		merged := cloneMap(old)
		for k, v := range props {
			merged[k] = v
		}
		encoded, err := knowledgeGraphJSON(merged)
		if err != nil {
			return nil, err
		}
		if err := dao.Update(item.ID, &store.TopologyRelation{SrcID: srcID, DstID: dstID, Type: edgeType, PropsJSON: encoded}); err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": item.ID, "created": false}, nil
	}
	encoded, err := knowledgeGraphJSON(props)
	if err != nil {
		return nil, err
	}
	id, err := dao.Create(&store.TopologyRelation{SrcID: srcID, DstID: dstID, Type: edgeType, PropsJSON: encoded})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": id, "created": true}, nil
}

func knowledgeGraphBatchUpsert(ops []knowledgeGraphMutation) (map[string]interface{}, error) {
	if len(ops) > 5000 {
		return nil, errors.New("too many graph mutations")
	}
	result := map[string]interface{}{"results": make([]map[string]interface{}, 0, len(ops))}
	for _, op := range ops {
		var item map[string]interface{}
		var err error
		switch op.Operation {
		case "upsert_node":
			item, err = knowledgeGraphUpsertNode(op.Type, op.Name, op.Props)
		case "upsert_edge":
			item, err = knowledgeGraphUpsertEdge(op.SrcID, op.DstID, op.EdgeType, op.EdgeProps)
		default:
			err = errors.New("invalid graph mutation")
		}
		if err != nil {
			return nil, err
		}
		result["results"] = append(result["results"].([]map[string]interface{}), item)
	}
	return result, nil
}

func knowledgeGraphSnapshot(clusterID string) (map[string]interface{}, error) {
	nodes, _, err := (&store.TopologyNodeDAO{}).List("", "", 100000, 0)
	if err != nil {
		return nil, err
	}
	edges, _, err := (&store.TopologyRelationDAO{}).List(0, 0, "", 100000, 0)
	if err != nil {
		return nil, err
	}
	nodeOut := make([]map[string]interface{}, 0, len(nodes))
	for _, node := range nodes {
		props := knowledgeGraphProps(node.PropsJSON)
		if !knowledgeGraphSameCluster(props, clusterID) {
			continue
		}
		nodeOut = append(nodeOut, map[string]interface{}{
			"id": node.ID, "type": node.Type, "name": node.Name, "props": props,
			"cluster_id": knowledgeGraphCluster(props), "created_by": props["created_by"],
		})
	}
	edgeOut := make([]map[string]interface{}, 0, len(edges))
	for _, edge := range edges {
		props := knowledgeGraphProps(edge.PropsJSON)
		if !knowledgeGraphSameCluster(props, clusterID) {
			continue
		}
		edgeOut = append(edgeOut, map[string]interface{}{
			"id": edge.ID, "src_id": edge.SrcID, "dst_id": edge.DstID,
			"type": edge.Type, "props": props,
		})
	}
	return map[string]interface{}{"nodes": nodeOut, "edges": edgeOut}, nil
}

func knowledgeGraphFindNode(typeName, name, clusterID string) (map[string]interface{}, error) {
	snapshot, err := knowledgeGraphSnapshot(clusterID)
	if err != nil {
		return nil, err
	}
	for _, node := range snapshot["nodes"].([]map[string]interface{}) {
		if node["type"] == typeName && node["name"] == name {
			return map[string]interface{}{"entity": node}, nil
		}
	}
	return map[string]interface{}{"entity": nil}, nil
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func knowledgeGraphReconcile() (map[string]interface{}, error) {
	// Stale marking remains a persistence concern and is intentionally executed
	// here, never from the orchestrator process.
	conn := store.GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT id, props_json FROM topology_nodes WHERE updated_at < NOW() - INTERVAL 7 DAY AND id NOT IN (SELECT src_id FROM topology_relations UNION SELECT dst_id FROM topology_relations)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marked := 0
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		props := knowledgeGraphProps(raw)
		if props["created_by"] != "auto" || props["status"] == "stale" {
			continue
		}
		props["status"] = "stale"
		encoded, err := knowledgeGraphJSON(props)
		if err != nil {
			return nil, err
		}
		if _, err := conn.Exec("UPDATE topology_nodes SET props_json=?, updated_at=updated_at WHERE id=?", encoded, id); err != nil {
			return nil, err
		}
		marked++
	}
	return map[string]interface{}{"marked": marked}, rows.Err()
}
