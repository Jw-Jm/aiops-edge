package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

// RunGraphContext returns the immutable/versioned graph context persisted for
// an investigation Run. Terminal Runs prefer the final context; active Runs
// receive the newest available context.
func (h *Handler) RunGraphContext(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.TenantID == "" {
		respondGraphError(w, "GRAPH_SCOPE_DENIED", "graph authorization is required")
		return
	}
	runID := runGraphContextID(r.URL.Path)
	if runID == "" || h.runDAO == nil || h.runGraphDAO == nil {
		respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "run_id is required")
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondGraphError(w, "ENTITY_NOT_FOUND", "run not found")
		return
	}
	if run.TenantID != auth.TenantID {
		respondGraphError(w, "GRAPH_SCOPE_DENIED", "run is outside tenant scope")
		return
	}
	contextValue, err := h.runGraphDAO.GetLatest(runID, auth.TenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondGraphError(w, "ENTITY_NOT_FOUND", "graph context not found")
		} else {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
		}
		return
	}
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(contextValue.ContextJSON), &response); err != nil || response == nil {
		respondGraphError(w, graphpkg.ErrGraphUnavailable, "stored graph context is invalid")
		return
	}
	response["run_id"] = contextValue.RunID
	response["context_version"] = contextValue.ContextVersion
	response["graph_schema_version"] = contextValue.GraphSchemaVersion
	response["graph_generation"] = contextValue.GraphGeneration
	response["trigger_entity_uid"] = contextValue.TriggerEntityUID
	response["root_cause_entity_uid"] = contextValue.RootCauseEntityUID
	response["partial"] = valueOrFalse(response, "partial")
	respondJSON(w, http.StatusOK, response)
}

func runGraphContextID(path string) string {
	const prefix = "/api/v1/ai/runs/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/graph-context") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/graph-context")
	if id == "" || strings.Contains(id, "/") || strings.TrimSpace(id) != id {
		return ""
	}
	return id
}

func valueOrFalse(values map[string]interface{}, key string) bool {
	value, ok := values[key].(bool)
	return ok && value
}
