package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ---------- 校验纯函数（可单测，不依赖 MySQL） ----------

// validDirections 允许的关系方向集合。
var validDirections = map[string]bool{
	"src_to_dst":     true,
	"dst_to_src":     true,
	"bidirectional":  true,
}

// validSemanticsTags 允许的关系语义标签集合（对齐 ongrid 推理层）。
var validSemanticsTags = map[string]bool{
	"hard_dep":    true,
	"runtime_dep": true,
	"aggregation": true,
	"redundancy":  true,
	"observation": true,
	"traffic":     true,
	"annotation":  true,
}

// validateTopologyNodeType 校验节点类型注册输入。
func validateTopologyNodeType(nt *store.TopologyNodeType) string {
	if nt.Name == "" {
		return "name required"
	}
	if nt.Tier < 0 {
		return "tier must be >= 0"
	}
	return ""
}

// validateTopologyRelationType 校验关系类型注册输入。
func validateTopologyRelationType(rt *store.TopologyRelationType) string {
	if rt.Name == "" {
		return "name required"
	}
	if !validDirections[rt.Direction] {
		return "invalid direction (want src_to_dst|dst_to_src|bidirectional)"
	}
	if !validSemanticsTags[rt.SemanticsTag] {
		return "invalid semantics_tag (want hard_dep|runtime_dep|aggregation|redundancy|observation|traffic|annotation)"
	}
	return ""
}

// ---------- Nodes ----------

// TopologyNodesRouter 分发 /api/v1/topology/nodes 的 CRUD。
func (h *Handler) TopologyNodesRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/topology/nodes"
	idStr := strings.TrimPrefix(r.URL.Path, base+"/")
	if idStr == r.URL.Path {
		idStr = ""
	}
	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			h.topologyNodeList(w, r)
		case http.MethodPost:
			h.topologyNodeCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.topologyNodeUpdate(w, r, id)
	case http.MethodDelete:
		h.topologyNodeDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) topologyNodeList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, total, err := (&store.TopologyNodeDAO{}).List(q.Get("type"), q.Get("q"), limit, offset)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"items": []store.TopologyNode{}, "total": 0, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"items": items, "total": total})
}

func (h *Handler) topologyNodeCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyNode
	json.Unmarshal(body, &req)
	if req.Type == "" || req.Name == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "type and name required"})
		return
	}
	id, err := (&store.TopologyNodeDAO{}).Create(&req)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true, "id": id})
}

func (h *Handler) topologyNodeUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyNode
	json.Unmarshal(body, &req)
	if err := (&store.TopologyNodeDAO{}).Update(id, &req); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) topologyNodeDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := (&store.TopologyNodeDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

// ---------- Relations ----------

// TopologyRelationsRouter 分发 /api/v1/topology/relations 的 CRUD。
func (h *Handler) TopologyRelationsRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/topology/relations"
	idStr := strings.TrimPrefix(r.URL.Path, base+"/")
	if idStr == r.URL.Path {
		idStr = ""
	}
	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			h.topologyRelationList(w, r)
		case http.MethodPost:
			h.topologyRelationCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.topologyRelationUpdate(w, r, id)
	case http.MethodDelete:
		h.topologyRelationDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) topologyRelationList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var srcID, dstID int64
	if v, _ := strconv.ParseInt(q.Get("src_id"), 10, 64); v > 0 {
		srcID = v
	}
	if v, _ := strconv.ParseInt(q.Get("dst_id"), 10, 64); v > 0 {
		dstID = v
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, total, err := (&store.TopologyRelationDAO{}).List(srcID, dstID, q.Get("type"), limit, offset)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"items": []store.TopologyRelation{}, "total": 0, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"items": items, "total": total})
}

func (h *Handler) topologyRelationCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyRelation
	json.Unmarshal(body, &req)
	if req.SrcID <= 0 || req.DstID <= 0 || req.Type == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "src_id, dst_id and type required"})
		return
	}
	id, err := (&store.TopologyRelationDAO{}).Create(&req)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true, "id": id})
}

func (h *Handler) topologyRelationUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyRelation
	json.Unmarshal(body, &req)
	if err := (&store.TopologyRelationDAO{}).Update(id, &req); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) topologyRelationDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := (&store.TopologyRelationDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

// ---------- Node Types ----------

// TopologyNodeTypesRouter 分发 /api/v1/topology/node-types 的 CRUD。
func (h *Handler) TopologyNodeTypesRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/topology/node-types"
	name := strings.TrimPrefix(r.URL.Path, base+"/")
	if name == r.URL.Path {
		name = ""
	}
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			h.topologyNodeTypeList(w, r)
		case http.MethodPost:
			h.topologyNodeTypeCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.topologyNodeTypeDelete(w, r, name)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) topologyNodeTypeList(w http.ResponseWriter, r *http.Request) {
	items, err := (&store.TopologyNodeTypeDAO{}).List()
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"items": []store.TopologyNodeType{}, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"items": items})
}

func (h *Handler) topologyNodeTypeCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyNodeType
	json.Unmarshal(body, &req)
	if msg := validateTopologyNodeType(&req); msg != "" {
		respondJSON(w, 400, map[string]interface{}{"error": msg})
		return
	}
	if err := (&store.TopologyNodeTypeDAO{}).Create(&req); err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) topologyNodeTypeDelete(w http.ResponseWriter, r *http.Request, name string) {
	nt, err := (&store.TopologyNodeTypeDAO{}).Get(name)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	if nt != nil && nt.Builtin {
		respondJSON(w, 400, map[string]interface{}{"error": "builtin node type cannot be deleted"})
		return
	}
	if err := (&store.TopologyNodeTypeDAO{}).Delete(name); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

// ---------- Sync catalog from trace ----------

// catalogTypeForService 将 trace 服务名映射到 ongrid 类型体系，使图谱能落在 tier 分层上。
// 服务→service(tier1)；db/cache/mq→cluster(tier2)。外部/网关视为 service。
func catalogTypeForService(name string) string {
	typ, _ := topologyNodeType(name)
	switch typ {
	case "db", "cache", "mq":
		return "cluster"
	default:
		return "service"
	}
}

// SyncTopologyCatalog 从 ClickHouse trace_spans 聚合服务节点 + 调用边，写入拓扑目录
// （topology_nodes / topology_relations，类型为 ongrid 体系）。幂等：同名节点 upsert。
// 返回写入的节点/边数量。
func (h *Handler) SyncTopologyCatalog(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// 节点聚合
	nodeSQL := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls FROM observability.trace_spans "+
			"WHERE tenant_id='%s' AND date >= today()-1 GROUP BY service_name ORDER BY calls DESC LIMIT 500", tid)
	nodeBody, err := h.queryClickHouse(ctx, nodeSQL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "trace query failed: "+err.Error())
		return
	}
	nodeRows, err := parseRows(nodeBody)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	nd := &store.TopologyNodeDAO{}
	nodeIDs := map[string]int64{}
	created := 0
	for _, nr := range nodeRows {
		name := fmt.Sprintf("%v", nr["service"])
		if name == "" || name == "<nil>" {
			continue
		}
		typ := catalogTypeForService(name)
		// upsert：先查是否有同 type+name 节点
		items, _, _ := nd.List(typ, name, 10, 0)
		var id int64
		if len(items) > 0 {
			id = items[0].ID
		} else {
			id, err = nd.Create(&store.TopologyNode{Type: typ, Name: name, PropsJSON: `{"source":"trace"}`})
			if err != nil {
				continue
			}
			created++
		}
		nodeIDs[name] = id
	}

	// 边聚合：trace_spans 无 parent_span_id，改用 trace_id 关联。
	// 同一 trace 内按各服务最早 start_time 排序，相邻服务建立调用边（时序早者→晚者）。
	edgeSQL := fmt.Sprintf(
		"SELECT trace_id, service_name, toUnixTimestamp(min(start_time)) AS first_ts "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND date >= today()-1 "+
			"GROUP BY trace_id, service_name", tid)
	edgeBody, err := h.queryClickHouse(ctx, edgeSQL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "trace edge query failed: "+err.Error())
		return
	}
	edgeRows, err := parseRows(edgeBody)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	// 按 trace 分组，服务按时序排序
	type svcTs struct {
		name string
		ts   int64
	}
	traceSvcs := map[string][]svcTs{}
	for _, er := range edgeRows {
		traceID := fmt.Sprintf("%v", er["trace_id"])
		svc := fmt.Sprintf("%v", er["service_name"])
		if svc == "" || svc == "<nil>" {
			continue
		}
		ts, _ := toInt64(er["first_ts"])
		traceSvcs[traceID] = append(traceSvcs[traceID], svcTs{name: svc, ts: ts})
	}

	rd := &store.TopologyRelationDAO{}
	relCreated := 0
	seen := map[string]bool{}
	for _, svcs := range traceSvcs {
		// 按时序排序
		for i := 0; i < len(svcs); i++ {
			for j := i + 1; j < len(svcs); j++ {
				if svcs[j].ts < svcs[i].ts {
					svcs[i], svcs[j] = svcs[j], svcs[i]
				}
			}
		}
		// 相邻服务建边（去重，覆盖不同方向计为同一条 depends_on）
		for i := 0; i < len(svcs)-1; i++ {
			src, dst := svcs[i].name, svcs[i+1].name
			if src == dst {
				continue
			}
			key := src + ">" + dst
			if seen[key] {
				continue
			}
			seen[key] = true
			srcID, ok1 := nodeIDs[src]
			dstID, ok2 := nodeIDs[dst]
			if !ok1 || !ok2 || srcID == dstID {
				continue
			}
			// 幂等：已存在的 (src,dst,depends_on) 跳过
			relList, _, _ := rd.List(srcID, dstID, "depends_on", 10, 0)
			if len(relList) > 0 {
				continue
			}
			if _, err := rd.Create(&store.TopologyRelation{SrcID: srcID, DstID: dstID, Type: "depends_on", PropsJSON: `{"source":"trace"}`}); err == nil {
				relCreated++
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"nodes":         len(nodeIDs),
		"nodes_created": created,
		"edges_created": relCreated,
	})
}

// ---------- Relation Types ----------

// TopologyRelationTypesRouter 分发 /api/v1/topology/relation-types 的 CRUD。
func (h *Handler) TopologyRelationTypesRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/topology/relation-types"
	name := strings.TrimPrefix(r.URL.Path, base+"/")
	if name == r.URL.Path {
		name = ""
	}
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			h.topologyRelationTypeList(w, r)
		case http.MethodPost:
			h.topologyRelationTypeCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.topologyRelationTypeDelete(w, r, name)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) topologyRelationTypeList(w http.ResponseWriter, r *http.Request) {
	items, err := (&store.TopologyRelationTypeDAO{}).List()
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"items": []store.TopologyRelationType{}, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"items": items})
}

func (h *Handler) topologyRelationTypeCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req store.TopologyRelationType
	json.Unmarshal(body, &req)
	if msg := validateTopologyRelationType(&req); msg != "" {
		respondJSON(w, 400, map[string]interface{}{"error": msg})
		return
	}
	if err := (&store.TopologyRelationTypeDAO{}).Create(&req); err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) topologyRelationTypeDelete(w http.ResponseWriter, r *http.Request, name string) {
	rt, err := (&store.TopologyRelationTypeDAO{}).Get(name)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	if rt != nil && rt.Builtin {
		respondJSON(w, 400, map[string]interface{}{"error": "builtin relation type cannot be deleted"})
		return
	}
	if err := (&store.TopologyRelationTypeDAO{}).Delete(name); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}
