package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// GraphOpsRouter exposes audited, server-side admin operations for projection
// health. It never exposes HugeGraph credentials or an arbitrary graph query.
func (h *Handler) GraphOpsRouter(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin") {
		respondGraphError(w, "GRAPH_SCOPE_DENIED", "administrator role required")
		return
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/sync-states") {
		auth, err := RequestAuthorizationContext(r)
		if err != nil {
			respondGraphAuthorizationError(w, err)
			return
		}
		items, err := (&store.GraphSyncStateDAO{}).List(auth.TenantID, 100)
		if err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "count": len(items)})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/outbox") && r.Method == http.MethodGet {
		items, err := (&store.GraphProjectionOutboxDAO{}).List(100)
		if err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "count": len(items)})
		return
	}
	if strings.Contains(r.URL.Path, "/outbox/") && strings.HasSuffix(r.URL.Path, "/retry") && r.Method == http.MethodPost {
		id, err := graphOpsID(r.URL.Path, "/outbox/", "/retry")
		if err != nil {
			respondGraphError(w, "GRAPH_INVALID_ARGUMENT", err.Error())
			return
		}
		if err := (&store.GraphProjectionOutboxDAO{}).RetryByID(id); err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		auditWrite(r, "graph.outbox.retry", strconv.FormatInt(id, 10), "{}")
		respondJSON(w, http.StatusOK, map[string]interface{}{"retried": true, "id": id})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/aliases") && r.Method == http.MethodGet {
		auth, err := RequestAuthorizationContext(r)
		if err != nil {
			respondGraphAuthorizationError(w, err)
			return
		}
		aliases, err := (&store.GraphEntityAliasDAO{}).ListByTenant(auth.TenantID, 100)
		if err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": aliases, "count": len(aliases)})
		return
	}
	if strings.Contains(r.URL.Path, "/aliases/") && strings.HasSuffix(r.URL.Path, "/resolve") && r.Method == http.MethodPost {
		id, err := graphOpsID(r.URL.Path, "/aliases/", "/resolve")
		if err != nil {
			respondGraphError(w, "GRAPH_INVALID_ARGUMENT", err.Error())
			return
		}
		var request struct {
			CanonicalEntityUID string `json:"canonical_entity_uid"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request) != nil || request.CanonicalEntityUID == "" {
			respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "canonical_entity_uid is required")
			return
		}
		if err := (&store.GraphEntityAliasDAO{}).ResolveConflict(id, request.CanonicalEntityUID); err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		auditWrite(r, "graph.alias.resolve", strconv.FormatInt(id, 10), request.CanonicalEntityUID)
		respondJSON(w, http.StatusOK, map[string]interface{}{"resolved": true, "id": id})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/reconcile-runs") && r.Method == http.MethodGet {
		auth, err := RequestAuthorizationContext(r)
		if err != nil {
			respondGraphAuthorizationError(w, err)
			return
		}
		items, err := (&store.GraphReconcileRunDAO{}).List(auth.TenantID, 100)
		if err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "count": len(items)})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/shadow-diff") && r.Method == http.MethodGet {
		auth, err := RequestAuthorizationContext(r)
		if err != nil {
			respondGraphAuthorizationError(w, err)
			return
		}
		items, err := (&store.GraphShadowDiffDAO{}).List(auth.TenantID, 100)
		if err != nil {
			respondGraphError(w, graphpkg.ErrGraphUnavailable, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "count": len(items)})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/reconcile") && r.Method == http.MethodPost {
		auditWrite(r, "graph.reconcile.request", "graph", "{}")
		respondJSON(w, http.StatusAccepted, map[string]interface{}{"accepted": true})
		return
	}
	respondGraphError(w, "GRAPH_INVALID_ARGUMENT", "unsupported graph ops route")
}

func graphOpsID(path, prefix, suffix string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/ai/kg/ops"), suffix)
	value = strings.TrimPrefix(value, prefix)
	if value == "" || strings.Contains(value, "/") {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(value, 10, 64)
}
