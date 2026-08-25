package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	knowledgeGraphReadCapability  = "control_plane.knowledge_graph.read"
	knowledgeGraphWriteCapability = "control_plane.knowledge_graph.write"
)

type knowledgeGraphRequest struct {
	Operation  string                   `json:"operation"`
	ClusterID  string                   `json:"cluster_id"`
	Type       string                   `json:"type"`
	Name       string                   `json:"name"`
	Props      map[string]interface{}   `json:"props"`
	SrcID      int64                    `json:"src_id"`
	DstID      int64                    `json:"dst_id"`
	EdgeType   string                   `json:"edge_type"`
	EdgeProps  map[string]interface{}   `json:"edge_props"`
	NodeID     int64                    `json:"node_id"`
	Hops       int                      `json:"hops"`
	Depth      int                      `json:"depth"`
	EdgeTypes  []string                 `json:"edge_types"`
	FromType   string                   `json:"from_type"`
	FromName   string                   `json:"from_name"`
	ToType     string                   `json:"to_type"`
	ToName     string                   `json:"to_name"`
	Operations []knowledgeGraphMutation `json:"operations"`
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
	if req.Operation == "" || (req.Operation != "snapshot" && req.Operation != "find_node" && req.Operation != "upsert_node" && req.Operation != "upsert_edge" && req.Operation != "batch_upsert" && req.Operation != "reconcile") {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	capability := knowledgeGraphReadCapability
	if req.Operation == "upsert_node" || req.Operation == "upsert_edge" || req.Operation == "batch_upsert" || req.Operation == "reconcile" {
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
