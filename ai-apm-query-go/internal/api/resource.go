package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/biz"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ResolveResource resolves a tenant-authorized resource locator to the strict
// canonical ResourceRef contract. It is intentionally the first new
// cluster-scoped query-api route; legacy routes remain fail-closed until their
// callers are migrated to canonical context.
func (h *Handler) ResolveResource(w http.ResponseWriter, r *http.Request) {
	authorization, err := RequestAuthorizationContext(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	query := biz.ResourceQuery{
		TenantID:     authorization.TenantID,
		ClusterRef:   strings.TrimSpace(r.URL.Query().Get("cluster_id")),
		ResourceType: strings.TrimSpace(r.URL.Query().Get("type")),
		Namespace:    strings.TrimSpace(r.URL.Query().Get("namespace")),
		Name:         strings.TrimSpace(r.URL.Query().Get("name")),
	}
	resource, err := (biz.ResourceResolver{}).Resolve(query)
	if err != nil {
		switch {
		case errors.Is(err, biz.ErrInvalidContext):
			respondResourceError(w, http.StatusBadRequest, "invalid_context")
		case errors.Is(err, store.ErrClusterAmbiguous):
			respondResourceError(w, http.StatusConflict, "ambiguous_resource")
		case errors.Is(err, store.ErrClusterNotFound):
			respondResourceError(w, http.StatusNotFound, "resource_not_found")
		default:
			respondResourceError(w, http.StatusServiceUnavailable, "cluster_unavailable")
		}
		return
	}
	if resource.Namespace == nil || *resource.Namespace == "" {
		respondResourceError(w, http.StatusBadRequest, "invalid_context")
		return
	}
	decision, err := (&store.AuthorizationDAO{}).Authorize(store.AuthorizationQuery{
		UserID: authorization.UserID, SessionID: authorization.SessionID, TenantRef: authorization.TenantID,
		ClusterRef: resource.ClusterID, Namespace: *resource.Namespace, ResourceType: resource.ResourceType,
		ResourceName: resource.Name, Action: "kubernetes.read",
	})
	if err != nil || !decision.Allowed {
		if err != nil || decision.DenialCode == store.DenialMySQLUnavailable {
			respondResourceError(w, http.StatusServiceUnavailable, "cluster_unavailable")
			return
		}
		respondResourceError(w, http.StatusForbidden, "permission_denied")
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"data": resource})
}

func respondAuthorizationError(w http.ResponseWriter, err error) {
	switch {
	case isAuthorizationError(err, "invalid_context"):
		respondResourceError(w, http.StatusBadRequest, "invalid_context")
	case isAuthorizationError(err, "cluster_unavailable"):
		respondResourceError(w, http.StatusServiceUnavailable, "cluster_unavailable")
	default:
		respondResourceError(w, http.StatusForbidden, "permission_denied")
	}
}

func respondResourceError(w http.ResponseWriter, status int, code string) {
	respondJSON(w, status, map[string]string{"error": code})
}
