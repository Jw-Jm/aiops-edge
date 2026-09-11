package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// GraphPublicRouter owns the browser-facing graph API. It is intentionally a
// typed router: no graph query language or HugeGraph URL is accepted here.
func (h *Handler) GraphPublicRouter(w http.ResponseWriter, r *http.Request) {
	for _, key := range []string{"gremlin", "cypher", "sql", "promql", "raw_filter"} {
		if _, present := r.URL.Query()[key]; present {
			respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "raw graph/query language parameters are not allowed")
			return
		}
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/ai/kg/health" {
		if h == nil || h.graphRepo == nil || h.graphInitErr != nil {
			respondJSON(w, http.StatusServiceUnavailable, publicGraphHealth(graphpkg.GraphHealth{Ready: false, Backend: "unconfigured", SchemaVersion: graphpkg.GraphSchemaVersion, ErrorCode: graphpkg.ErrGraphUnavailable}))
			return
		}
		respondJSON(w, http.StatusOK, publicGraphHealth(h.graphRepo.Health(r.Context())))
		return
	}
	if h == nil || h.graphRepo == nil || h.graphInitErr != nil {
		respondGraphError(w, graphpkg.ErrGraphUnavailable, "knowledge graph is not configured")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/ai/kg/entities/search" {
		h.graphSearch(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/neighbors") && r.Method == http.MethodGet {
		h.graphNeighbors(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/candidate") && r.Method == http.MethodGet {
		h.graphCandidate(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/impact") && r.Method == http.MethodGet {
		h.graphImpact(w, r)
		return
	}
	if r.URL.Path == "/api/v1/ai/kg/path" && r.Method == http.MethodPost {
		h.graphPath(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/ai/kg/entities/") && r.Method == http.MethodGet {
		h.graphEntity(w, r)
		return
	}
	respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "unsupported graph route")
}

func (h *Handler) graphEntity(w http.ResponseWriter, r *http.Request) {
	uid, suffix, ok := graphEntityPath(r.URL.Path)
	if !ok || suffix != "" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "invalid entity path")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	entity, err := h.graphRepo.GetEntity(r.Context(), scope, uid)
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, entity)
}

// operationsGraphSearchTypes 是默认运维图谱可搜索类型：八类容器主资源、
// KubeVirt VM/VMI、Kubernetes Node、物理机，以及真实的存储/网络依赖。
// Container、ReplicaSet、EndpointSlice 只作为详情/证据，业务 service、
// application、middleware 只作兼容数据，均不出现在默认搜索 profile。
var operationsGraphSearchTypes = map[string]struct{}{
	"deployment": {}, "statefulset": {}, "daemonset": {}, "job": {}, "cronjob": {}, "pod": {}, "k8s_service": {}, "ingress": {},
	"vm": {}, "vmi": {}, "k8s_node": {}, "physical_server": {},
	"pvc": {}, "pv": {}, "storage_class": {}, "data_volume": {}, "volume": {}, "disk_device": {},
	"nad": {}, "network": {}, "virtual_interface": {}, "cni": {}, "nic": {}, "switch": {}, "switch_port": {},
}

// graphOperationsProfileCandidateLimit 是 operations profile 的 alias 候选上限：
// 必须先取候选再按 profile 过滤，否则前端过滤会让合法结果被提前截断。
const graphOperationsProfileCandidateLimit = 50

// graphSearchProfileAllows 判断某实体类型是否属于指定搜索 profile。
// 空 profile 表示兼容读取（不过滤）；未知 profile 在 handler 层直接拒绝。
func graphSearchProfileAllows(profile, entityType string) bool {
	if profile == "" {
		return true
	}
	if profile != "operations" {
		return false
	}
	_, ok := operationsGraphSearchTypes[entityType]
	return ok
}

func (h *Handler) graphSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 || len([]rune(q)) > 128 {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "q must contain 2 to 128 characters")
		return
	}
	limit, err := graphIntQuery(r, "limit", 20, 1, 50)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profile != "" && profile != "operations" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "unknown graph search profile")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	entityType := strings.TrimSpace(r.URL.Query().Get("entity_type"))
	items, err := h.searchGraphAliases(r.Context(), scope, entityType, profile, q, limit)
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "count": len(items)})
}

func (h *Handler) graphNeighbors(w http.ResponseWriter, r *http.Request) {
	uid, suffix, ok := graphEntityPath(r.URL.Path)
	if !ok || suffix != "neighbors" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "invalid neighbor path")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	depth, err := graphIntQuery(r, "depth", 1, 1, graphpkg.DefaultPublicMaxDepth)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	vertices, err := graphIntQuery(r, "max_vertices", graphpkg.DefaultPublicMaxVertices, 1, graphpkg.DefaultPublicMaxVertices)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	edges, err := graphIntQuery(r, "max_edges", graphpkg.DefaultPublicMaxEdges, 1, graphpkg.DefaultPublicMaxEdges)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	result, err := h.graphRepo.Neighbors(r.Context(), scope, graphpkg.NeighborQuery{CenterEntityUID: uid, MaxDepth: depth, MaxVertices: vertices, MaxEdges: edges, Direction: graphDirection(r.URL.Query().Get("direction")), RelationTypes: graphCSV(r.URL.Query().Get("relation_types"))})
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) graphCandidate(w http.ResponseWriter, r *http.Request) {
	uid, suffix, ok := graphEntityPath(r.URL.Path)
	if !ok || suffix != "candidate" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "invalid candidate path")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	depth, err := graphIntQuery(r, "depth", graphpkg.DefaultPublicMaxDepth, 1, graphpkg.DefaultPublicMaxDepth)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	vertices, err := graphIntQuery(r, "max_vertices", graphpkg.DefaultPublicMaxVertices, 1, graphpkg.DefaultPublicMaxVertices)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	edges, err := graphIntQuery(r, "max_edges", graphpkg.DefaultPublicMaxEdges, 1, graphpkg.DefaultPublicMaxEdges)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	result, err := h.graphRepo.CandidateSubgraph(r.Context(), scope, graphpkg.NeighborQuery{CenterEntityUID: uid, MaxDepth: depth, MaxVertices: vertices, MaxEdges: edges})
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) graphImpact(w http.ResponseWriter, r *http.Request) {
	uid, suffix, ok := graphEntityPath(r.URL.Path)
	if !ok || suffix != "impact" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "invalid impact path")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	depth, err := graphIntQuery(r, "max_depth", graphpkg.DefaultInternalMaxDepth, 1, graphpkg.DefaultInternalMaxDepth)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	result, err := h.graphRepo.Impact(r.Context(), scope, graphpkg.ImpactQuery{RootUID: uid, MaxDepth: depth, MaxVertices: graphpkg.DefaultPublicMaxVertices, MaxEdges: graphpkg.DefaultPublicMaxEdges})
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) graphPath(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SourceUID     string   `json:"source_entity_uid"`
		TargetUID     string   `json:"target_entity_uid"`
		MaxDepth      int      `json:"max_depth"`
		RelationTypes []string `json:"relation_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || request.SourceUID == "" || request.TargetUID == "" {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "source_entity_uid and target_entity_uid are required")
		return
	}
	if request.MaxDepth == 0 {
		request.MaxDepth = graphpkg.DefaultInternalMaxDepth
	}
	if request.MaxDepth < 1 || request.MaxDepth > graphpkg.DefaultInternalMaxDepth {
		respondGraphError(w, graphpkg.ErrGraphQueryLimitExceeded, "max_depth exceeds public graph limit")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	result, err := h.graphRepo.ShortestPath(r.Context(), scope, graphpkg.PathQuery{SourceUID: request.SourceUID, TargetUID: request.TargetUID, MaxDepth: request.MaxDepth, MaxVertices: graphpkg.DefaultPublicMaxVertices, MaxEdges: graphpkg.DefaultPublicMaxEdges, RelationTypes: request.RelationTypes})
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) graphScope(r *http.Request) (graphpkg.GraphScope, error) {
	authContext, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		authContext, err = RequestAuthorizationContext(r)
		if err != nil {
			return graphpkg.GraphScope{}, err
		}
	}
	// Scope precedence: explicit request parameter/header (validated against
	// the tenant below), then the server-persisted session scope selected
	// through POST /me/scope so browser flows are not forced to repeat it.
	clusterID := strings.TrimSpace(firstNonEmpty(r.Header.Get("X-Cluster-ID"), r.URL.Query().Get("cluster_id"), authContext.ActiveClusterID))
	scope := graphpkg.GraphScope{TenantID: authContext.TenantID, ClusterIDs: map[string]struct{}{}}
	if clusterID == "" {
		return scope, nil
	}
	if db := store.GetDB(); db != nil {
		cluster, err := (&store.ClusterDAO{}).ResolveRef(authContext.TenantID, clusterID)
		if err != nil || cluster == nil || cluster.TenantID != authContext.TenantID {
			return graphpkg.GraphScope{}, errors.New("GRAPH_SCOPE_DENIED")
		}
		clusterID = cluster.ClusterID
	}
	scope.ClusterIDs[clusterID] = struct{}{}
	return scope, nil
}

func (h *Handler) searchGraphAliases(ctx context.Context, scope graphpkg.GraphScope, entityType, profile, query string, limit int) ([]graphpkg.Entity, error) {
	// profile 过滤必须发生在调用方 limit 截断之前，否则被 profile 排除的候选
	// 会先占满 limit，使合法结果被静默丢弃。
	candidateLimit := limit
	if profile == "operations" && candidateLimit < graphOperationsProfileCandidateLimit {
		candidateLimit = graphOperationsProfileCandidateLimit
	}
	if h.graphAliasDAO != nil {
		if aliases, err := h.graphAliasDAO.Search(scope.TenantID, firstScopeClusterID(scope), graphpkg.NameKeyV1(query), candidateLimit); err == nil && len(aliases) > 0 {
			items := make([]graphpkg.Entity, 0, limit)
			seen := map[string]struct{}{}
			for _, alias := range aliases {
				if _, ok := seen[alias.CanonicalEntityUID]; ok {
					continue
				}
				entity, getErr := h.graphRepo.GetEntity(ctx, scope, alias.CanonicalEntityUID)
				if getErr != nil {
					return nil, getErr
				}
				seen[entity.EntityUID] = struct{}{}
				if entityType != "" && entity.EntityType != entityType {
					continue
				}
				if !graphSearchProfileAllows(profile, entity.EntityType) {
					continue
				}
				items = append(items, entity)
				if len(items) == limit {
					break
				}
			}
			return items, nil
		}
	}
	if _, ok := h.graphRepo.(*graphpkg.MemoryRepository); ok {
		entities, err := h.graphRepo.SearchEntities(ctx, scope, graphpkg.EntitySearchQuery{EntityType: entityType, Name: query, Limit: candidateLimit})
		if err != nil {
			return nil, err
		}
		items := make([]graphpkg.Entity, 0, limit)
		for _, entity := range entities {
			if !graphSearchProfileAllows(profile, entity.EntityType) {
				continue
			}
			items = append(items, entity)
			if len(items) == limit {
				break
			}
		}
		return items, nil
	}
	return nil, graphpkgError(graphpkg.ErrGraphFeatureUnavailable, "graph_entity_alias is unavailable")
}

func graphEntityPath(path string) (string, string, bool) {
	const prefix = "/api/v1/ai/kg/entities/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	uid, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	suffix := ""
	if len(parts) > 1 {
		if len(parts) != 2 {
			return "", "", false
		}
		suffix = parts[1]
	}
	return uid, suffix, true
}

func graphIntQuery(r *http.Request, name string, fallback, min, max int) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min {
		return 0, errors.New(name + " is outside the allowed range")
	}
	if parsed > max {
		return 0, graphpkg.NewError(graphpkg.ErrGraphQueryLimitExceeded, name+" exceeds the server limit")
	}
	return parsed, nil
}

func graphCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.ToUpper(strings.TrimSpace(part)); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func graphDirection(raw string) string {
	direction := strings.ToUpper(strings.TrimSpace(raw))
	if direction == "OUT" || direction == "IN" {
		return direction
	}
	return "BOTH"
}

func firstScopeClusterID(scope graphpkg.GraphScope) string {
	for value := range scope.ClusterIDs {
		return value
	}
	return ""
}

func publicGraphHealth(health graphpkg.GraphHealth) map[string]interface{} {
	return map[string]interface{}{"ready": health.Ready, "backend": health.Backend, "schema_version": health.SchemaVersion}
}

func respondGraphAuthorizationError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "GRAPH_SCOPE_DENIED") {
		respondGraphError(w, "GRAPH_SCOPE_DENIED", "graph scope is not authorized")
		return
	}
	respondGraphError(w, "GRAPH_SCOPE_DENIED", "graph authorization is required")
}

func respondGraphErrorFromGo(w http.ResponseWriter, err error) {
	var graphErr *graphpkg.Error
	if errors.As(err, &graphErr) {
		code := graphErr.Code
		switch code {
		case graphpkg.ErrGraphScopeViolation:
			code = "GRAPH_SCOPE_DENIED"
		case graphpkg.ErrGraphEntityNotFound:
			code = "ENTITY_NOT_FOUND"
		case graphpkg.ErrGraphEmpty:
			code = "ENTITY_NOT_FOUND"
		case graphpkg.ErrUnknownEntityType, graphpkg.ErrOntologyViolation:
			// preserve contract code
		}
		respondGraphError(w, code, graphErr.Message)
		return
	}
	respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
}

// publicGraphErrorMessage keeps backend diagnostics out of browser responses.
// Graph adapters may wrap HTTP bodies, URLs or datastore errors in the
// GRAPH_UNAVAILABLE/SCHEMA/FEATURE codes; those details belong in service
// logs, while the public contract exposes only a stable code and generic text.
func publicGraphErrorMessage(code, message string) string {
	switch code {
	case graphpkg.ErrGraphUnavailable:
		return "knowledge graph is unavailable"
	case graphpkg.ErrGraphSchemaMismatch:
		return "knowledge graph schema is incompatible"
	case graphpkg.ErrGraphFeatureUnavailable:
		return "graph operation is unavailable"
	default:
		return message
	}
}

func respondGraphError(w http.ResponseWriter, code, message string) {
	status := http.StatusBadRequest
	switch code {
	case "GRAPH_SCOPE_DENIED":
		status = http.StatusForbidden
	case "ENTITY_NOT_FOUND":
		status = http.StatusNotFound
	case "ENTITY_AMBIGUOUS", graphpkg.ErrGraphVersionConflict:
		status = http.StatusConflict
	case graphpkg.ErrGraphQueryLimitExceeded, graphpkg.ErrOntologyViolation, graphpkg.ErrUnknownEntityType:
		status = http.StatusUnprocessableEntity
	case graphpkg.ErrGraphUnavailable, graphpkg.ErrGraphSchemaMismatch, graphpkg.ErrGraphFeatureUnavailable:
		status = http.StatusServiceUnavailable
	}
	respondJSON(w, status, map[string]interface{}{"error": map[string]interface{}{"code": code, "message": publicGraphErrorMessage(code, message), "request_id": store.NewUUIDv4()}})
}

func respondGraphParamError(w http.ResponseWriter, err error) {
	var graphErr *graphpkg.Error
	if errors.As(err, &graphErr) {
		respondGraphErrorFromGo(w, err)
		return
	}
	respondGraphError(w, "GRAPH_INVALID_ARGUMENT", err.Error())
}

func graphpkgError(code, message string) error { return graphpkg.NewError(code, message) }
