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
	if runScopeDenied(w, r, run.TenantID, run.PrimaryClusterID) {
		return
	}
	contextValue, err := h.runGraphDAO.GetLatest(runID, auth.TenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A Run is authoritative even when its graph projection was not
			// generated (for example, a historical Run created before graph
			// persistence was enabled). Keep the Run detail usable and make the
			// missing optional projection explicit instead of returning a noisy 404.
			respondJSON(w, http.StatusOK, emptyRunGraphContext(runID))
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

func emptyRunGraphContext(runID string) map[string]interface{} {
	return map[string]interface{}{
		"run_id":               runID,
		"context_version":      0,
		"graph_schema_version": 0,
		"graph_generation":     0,
		"partial":              true,
		"status":               "not_generated",
		"warning_codes":        []string{"GRAPH_CONTEXT_NOT_GENERATED"},
		"message":              "该历史调查尚未生成 Graph Context",
		"vertices":             []interface{}{},
		"edges":                []interface{}{},
		"propagation_paths":    []interface{}{},
	}
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
