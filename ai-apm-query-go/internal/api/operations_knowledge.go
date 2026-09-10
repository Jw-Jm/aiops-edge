package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type knowledgePayload struct {
	ScopeType      store.KnowledgeScopeType `json:"scope_type"`
	ClusterID      string                   `json:"cluster_id,omitempty"`
	KnowledgeType  string                   `json:"knowledge_type"`
	Title          string                   `json:"title"`
	Summary        string                   `json:"summary"`
	Content        string                   `json:"content"`
	SourceKind     string                   `json:"source_kind"`
	SourceRevision string                   `json:"source_revision,omitempty"`
}

type knowledgeReviewPayload struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func knowledgePath(path string) (clusterID, knowledgeID, action string, ok bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(path), "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "clusters" || parts[4] != "knowledge" {
		return "", "", "", false
	}
	clusterID = parts[3]
	if len(parts) == 5 {
		return clusterID, "", "", clusterID != ""
	}
	if len(parts) == 6 && parts[5] == "index-status" {
		return clusterID, "", "index-status", clusterID != ""
	}
	knowledgeID = parts[5]
	if len(parts) > 6 {
		action = parts[6]
	}
	return clusterID, knowledgeID, action, clusterID != "" && knowledgeID != ""
}

func knowledgeAuthScope(r *http.Request, clusterID string) (store.KnowledgeScope, AuthorizationContext, error) {
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		auth, err = RequestAuthorizationContext(r)
		if err != nil {
			return store.KnowledgeScope{}, AuthorizationContext{}, err
		}
	}
	if strings.TrimSpace(auth.TenantID) == "" || strings.TrimSpace(clusterID) == "" {
		return store.KnowledgeScope{}, auth, errors.New("knowledge scope is required")
	}
	if auth.ActiveClusterID != "" && auth.ActiveClusterID != clusterID {
		return store.KnowledgeScope{}, auth, authorizationFailure("CLUSTER_SCOPE_DENIED")
	}
	return store.KnowledgeScope{TenantID: auth.TenantID, ClusterID: clusterID}, auth, nil
}

func decodeKnowledgeBody(r *http.Request, target *knowledgePayload) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20+1))
	if err != nil {
		return err
	}
	if len(body) > 2<<20 {
		return errors.New("knowledge payload too large")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func decodeKnowledgeReviewBody(r *http.Request, target *knowledgeReviewPayload) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("review payload too large")
	}
	return json.Unmarshal(body, target)
}

func (h *Handler) OperationsKnowledgeRouter(w http.ResponseWriter, r *http.Request) {
	clusterID, knowledgeID, action, ok := knowledgePath(r.URL.Path)
	if !ok {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge path"})
		return
	}
	scope, auth, err := knowledgeAuthScope(r, clusterID)
	if err != nil {
		if isAuthorizationError(err, "CLUSTER_SCOPE_DENIED") {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "cluster_scope_denied"})
			return
		}
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	dao := &store.OperationsKnowledgeDAO{}
	switch {
	case knowledgeID == "" && action == "" && r.Method == http.MethodGet:
		items, listErr := dao.List(r.Context(), scope, r.URL.Query().Get("knowledge_type"), r.URL.Query().Get("status"), 50)
		if listErr != nil {
			respondKnowledgeStoreError(w, listErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": items, "meta": map[string]interface{}{"index_available": true}})
	case knowledgeID == "" && action == "" && r.Method == http.MethodPost:
		var payload knowledgePayload
		if err := decodeKnowledgeBody(r, &payload); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge payload"})
			return
		}
		payload.ClusterID = clusterID
		if payload.ScopeType == store.KnowledgePlatformCommon {
			payload.ClusterID = ""
			if !hasKnowledgeCapability(r, KnowledgeWriteCapability) {
				respondJSON(w, http.StatusForbidden, map[string]string{"error": KnowledgeWriteCapability + " required"})
				return
			}
		}
		item, version, createErr := dao.Create(r.Context(), scope, auth.UserID, store.KnowledgeInput{ScopeType: payload.ScopeType, ClusterID: payload.ClusterID, KnowledgeType: payload.KnowledgeType, Title: payload.Title, Summary: payload.Summary, Content: payload.Content, SourceKind: payload.SourceKind, SourceRevision: payload.SourceRevision})
		if createErr != nil {
			respondKnowledgeStoreError(w, createErr)
			return
		}
		respondJSON(w, http.StatusCreated, map[string]interface{}{"data": item, "version": version})
	case knowledgeID != "" && action == "" && r.Method == http.MethodGet:
		item, getErr := dao.Get(r.Context(), scope, knowledgeID)
		if getErr != nil {
			respondKnowledgeStoreError(w, getErr)
			return
		}
		versionID := item.CurrentVersionID
		if item.Status != store.KnowledgePublished && item.DraftVersionID != "" {
			versionID = item.DraftVersionID
		}
		version, versionErr := dao.GetVersion(r.Context(), scope, knowledgeID, versionID)
		if versionErr != nil {
			respondKnowledgeStoreError(w, versionErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"data": item, "version": version})
	case knowledgeID != "" && action == "" && (r.Method == http.MethodPut || r.Method == http.MethodPatch):
		var payload knowledgePayload
		if err := decodeKnowledgeBody(r, &payload); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge revision payload"})
			return
		}
		if !hasKnowledgeCapability(r, KnowledgeWriteCapability) {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": KnowledgeWriteCapability + " required"})
			return
		}
		version, revisionErr := dao.CreateRevision(r.Context(), scope, knowledgeID, auth.UserID, store.KnowledgeInput{Content: payload.Content, SourceKind: payload.SourceKind, SourceRevision: payload.SourceRevision})
		if revisionErr != nil {
			respondKnowledgeStoreError(w, revisionErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"version": version, "status": string(store.KnowledgeDraft)})
	case knowledgeID != "" && action == "submit" && r.Method == http.MethodPost:
		if submitErr := dao.Submit(r.Context(), scope, knowledgeID, auth.UserID); submitErr != nil {
			respondKnowledgeStoreError(w, submitErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": string(store.KnowledgePendingReview)})
	case knowledgeID != "" && action == "review" && r.Method == http.MethodPost:
		if !hasKnowledgeCapability(r, KnowledgeWriteCapability) {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": KnowledgeWriteCapability + " required"})
			return
		}
		var payload knowledgeReviewPayload
		if err := decodeKnowledgeReviewBody(r, &payload); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review payload"})
			return
		}
		if reviewErr := dao.Review(r.Context(), scope, knowledgeID, auth.UserID, payload.Decision, payload.Reason); reviewErr != nil {
			respondKnowledgeStoreError(w, reviewErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "reviewed"})
	case knowledgeID != "" && action == "disable" && r.Method == http.MethodPost:
		if !hasKnowledgeCapability(r, KnowledgeWriteCapability) {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": KnowledgeWriteCapability + " required"})
			return
		}
		if disableErr := dao.Disable(r.Context(), scope, knowledgeID, auth.UserID); disableErr != nil {
			respondKnowledgeStoreError(w, disableErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": string(store.KnowledgeDisabled)})
	case knowledgeID == "" && action == "index-status" && r.Method == http.MethodGet:
		states, stateErr := dao.ListIndexStates(r.Context(), scope, 100)
		if stateErr != nil {
			respondKnowledgeStoreError(w, stateErr)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"items": states})
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
	}
}

func respondKnowledgeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrMySQLUnavailable) {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "knowledge_store_unavailable"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "not found") {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "knowledge_not_found"})
		return
	}
	respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}
