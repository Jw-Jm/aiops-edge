package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

var graphInternalOperations = map[string]struct{}{
	"resolve_entity": {}, "get_vertex": {}, "neighbors": {}, "shortest_path": {},
	"candidate_subgraph": {}, "impact": {}, "evidence_context": {},
}

// InternalQueryGraph is the sole orchestrator graph read boundary. It shares
// the existing signed context, scope, ToolRun, idempotency and lease fencing
// path with the other canonical internal query endpoints.
func (h *Handler) InternalQueryGraph(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "knowledge.graph.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	if _, ok := graphInternalOperations[req.GraphOperation]; !ok {
		respondInternalQueryError(w, &internalQueryError{Code: "VALIDATION_FAILED", Message: "unsupported graph_operation"})
		return
	}
	if req.GraphOperation == "candidate_subgraph" && req.RelationPolicy != "" && req.RelationPolicy != "root_cause_candidate_v1" {
		respondGraphError(w, graphpkg.ErrOntologyViolation, "unsupported relation_policy")
		return
	}
	if req.GraphOperation == "impact" && req.RelationPolicy != "" && req.RelationPolicy != "failure_impact_v1" {
		respondGraphError(w, graphpkg.ErrOntologyViolation, "unsupported relation_policy")
		return
	}
	scope := graphpkg.GraphScope{TenantID: rctx.TenantID, ClusterIDs: map[string]struct{}{rctx.ClusterID: {}}}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		if h.graphRepo == nil || h.graphInitErr != nil {
			return nil, graphpkgError(graphpkg.ErrGraphUnavailable, "knowledge graph is not configured")
		}
		var value interface{}
		switch req.GraphOperation {
		case "resolve_entity":
			value, err = h.resolveGraphEntity(r.Context(), scope, req)
		case "get_vertex":
			if strings.TrimSpace(req.EntityUID) == "" {
				return nil, graphpkgError("GRAPH_INVALID_ARGUMENT", "entity_uid is required")
			}
			value, err = h.graphRepo.GetEntity(r.Context(), scope, req.EntityUID)
		case "neighbors":
			value, err = h.graphRepo.Neighbors(r.Context(), scope, graphpkg.NeighborQuery{CenterEntityUID: req.EntityUID, Direction: graphDirection(req.Direction), MaxDepth: req.MaxDepth, MaxVertices: req.MaxVertices, MaxEdges: req.MaxEdges, RelationTypes: req.RelationTypes})
		case "shortest_path":
			value, err = h.graphRepo.ShortestPath(r.Context(), scope, graphpkg.PathQuery{SourceUID: req.EntityUID, TargetUID: req.TargetEntityUID, MaxDepth: req.MaxDepth, MaxVertices: req.MaxVertices, MaxEdges: req.MaxEdges, RelationTypes: req.RelationTypes})
		case "candidate_subgraph":
			value, err = h.graphRepo.CandidateSubgraph(r.Context(), scope, graphpkg.NeighborQuery{CenterEntityUID: req.EntityUID, MaxDepth: req.MaxDepth, MaxVertices: req.MaxVertices, MaxEdges: req.MaxEdges, RelationTypes: req.RelationTypes})
		case "impact":
			value, err = h.graphRepo.Impact(r.Context(), scope, graphpkg.ImpactQuery{RootUID: req.EntityUID, MaxDepth: req.MaxDepth, MaxVertices: req.MaxVertices, MaxEdges: req.MaxEdges})
		case "evidence_context":
			value, err = h.getGraphContext(req.RunID, req.ContextVersion, rctx.TenantID)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
}

func (h *Handler) resolveGraphEntity(ctx context.Context, scope graphpkg.GraphScope, req *internalQueryRequest) (interface{}, error) {
	if req.EntityUID != "" {
		entity, err := h.graphRepo.GetEntity(ctx, scope, req.EntityUID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"entity": entity}, nil
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, graphpkgError("GRAPH_INVALID_ARGUMENT", "entity_uid or name is required")
	}
	items, err := h.searchGraphAliases(ctx, scope, req.EntityType, req.Name, 20)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, graphpkgError(graphpkg.ErrGraphEntityNotFound, req.Name)
	}
	if len(items) > 1 {
		return nil, graphpkgError("ENTITY_AMBIGUOUS", req.Name)
	}
	return map[string]interface{}{"entity": items[0]}, nil
}

func (h *Handler) getGraphContext(runID string, version int64, tenantID string) (interface{}, error) {
	if h.runGraphDAO == nil || runID == "" {
		return nil, graphpkgError(graphpkg.ErrGraphEntityNotFound, "graph context not found")
	}
	if version <= 0 {
		version = 1
	}
	value, err := h.runGraphDAO.Get(runID, version, tenantID)
	if err != nil {
		return nil, graphpkgError(graphpkg.ErrGraphUnavailable, err.Error())
	}
	if value.ContextJSON == "" {
		return value, nil
	}
	var contextValue interface{}
	if err := json.Unmarshal([]byte(value.ContextJSON), &contextValue); err != nil {
		return nil, err
	}
	return contextValue, nil
}
