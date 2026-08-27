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
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	entityType := strings.TrimSpace(r.URL.Query().Get("entity_type"))
	items, err := h.searchGraphAliases(r.Context(), scope, entityType, q, limit)
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
	clusterID := strings.TrimSpace(firstNonEmpty(r.Header.Get("X-Cluster-ID"), r.URL.Query().Get("cluster_id")))
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

func (h *Handler) searchGraphAliases(ctx context.Context, scope graphpkg.GraphScope, entityType, query string, limit int) ([]graphpkg.Entity, error) {
	if h.graphAliasDAO != nil {
		if aliases, err := h.graphAliasDAO.Search(scope.TenantID, firstScopeClusterID(scope), graphpkg.NameKeyV1(query), limit); err == nil && len(aliases) > 0 {
			items := make([]graphpkg.Entity, 0, len(aliases))
			seen := map[string]struct{}{}
			for _, alias := range aliases {
				if _, ok := seen[alias.CanonicalEntityUID]; ok {
					continue
				}
				entity, getErr := h.graphRepo.GetEntity(ctx, scope, alias.CanonicalEntityUID)
				if getErr != nil {
					return nil, getErr
				}
				if entityType == "" || entity.EntityType == entityType {
					items = append(items, entity)
					seen[entity.EntityUID] = struct{}{}
				}
			}
			return items, nil
		}
	}
	if _, ok := h.graphRepo.(*graphpkg.MemoryRepository); ok {
		return h.graphRepo.SearchEntities(ctx, scope, graphpkg.EntitySearchQuery{EntityType: entityType, Name: query, Limit: limit})
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
	respondJSON(w, status, map[string]interface{}{"error": map[string]interface{}{"code": code, "message": message, "request_id": store.NewUUIDv4()}})
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
