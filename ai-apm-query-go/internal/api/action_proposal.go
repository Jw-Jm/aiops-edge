package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ActionProposalRequest is the browser-facing request for a manual, canonical
// Kubernetes Action. Target identity is deliberately absent: query-api reads
// it through the K8s Access Boundary during preflight.
type ActionProposalRequest struct {
	IdempotencyKey string          `json:"idempotency_key"`
	ClusterID      string          `json:"cluster_id"`
	ResourceType   string          `json:"resource_type"`
	Namespace      string          `json:"namespace"`
	TargetName     string          `json:"target_name"`
	Operation      string          `json:"operation"`
	Params         json.RawMessage `json:"params"`
}

var canonicalK8sActionKinds = map[string]map[string]bool{
	"rollout_restart": {"deployment": true, "statefulset": true, "daemonset": true},
	"scale":           {"deployment": true, "statefulset": true},
	"delete_pod":      {"pod": true},
	"evict_pod":       {"pod": true},
	"cordon":          {"node": true},
	"uncordon":        {"node": true},
	"drain":           {"node": true},
}

// validateActionProposalRequest is the single browser proposal allowlist.
// It rejects fields that are not part of the operation's fixed mutation
// surface, so the approval hash cannot hide an additional side effect.
func validateActionProposalRequest(req ActionProposalRequest) error {
	req = normalizeActionProposalRequest(req)
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 255 {
		return errors.New("idempotency_key is required and must be at most 255 bytes")
	}
	if !canonicalUUID.MatchString(req.ClusterID) {
		return errors.New("cluster_id must be a canonical UUID")
	}
	if req.TargetName == "" || len(req.TargetName) > 253 {
		return errors.New("target_name is required and must be at most 253 bytes")
	}
	if req.ResourceType != "node" && req.Namespace == "" {
		return errors.New("namespace is required for namespaced targets")
	}
	allowedKinds, ok := canonicalK8sActionKinds[req.Operation]
	if !ok || !allowedKinds[req.ResourceType] {
		return fmt.Errorf("operation %q is not allowed for resource_type %q", req.Operation, req.ResourceType)
	}
	_, err := canonicalizeActionParams(req.Operation, req.Params)
	return err
}

// canonicalizeActionParams validates and produces the exact structured
// payload that is hashed and later consumed by the executor. The restart
// marker is fixed rather than generated at execution time so retries preserve
// the same action hash.
func canonicalizeActionParams(operation string, params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(params)))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("params must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("params must contain one JSON value")
		}
		return nil, errors.New("params must contain one valid JSON value")
	}
	switch operation {
	case "scale":
		if len(object) != 1 || object["replicas"] == nil {
			return nil, errors.New("scale requires only params.replicas")
		}
		if err := validateIntegerRange(object["replicas"], 0, 10000, "replicas"); err != nil {
			return nil, err
		}
	case "delete_pod", "evict_pod":
		if len(object) != 1 || object["grace_period_seconds"] == nil {
			return nil, errors.New("pod action requires only params.grace_period_seconds")
		}
		if err := validateIntegerRange(object["grace_period_seconds"], 0, 600, "grace_period_seconds"); err != nil {
			return nil, err
		}
	case "drain":
		if len(object) != 1 || object["drain_timeout"] == nil {
			return nil, errors.New("drain requires only params.drain_timeout")
		}
		if err := validateIntegerRange(object["drain_timeout"], 1, 3600, "drain_timeout"); err != nil {
			return nil, err
		}
	case "rollout_restart":
		if len(object) != 0 {
			return nil, errors.New("rollout_restart does not accept params")
		}
		return json.RawMessage(`{"metadata":{"annotations":{"aiops.observability.io/restartedAt":"requested"}}}`), nil
	default:
		if len(object) != 0 {
			return nil, errors.New("this operation does not accept params")
		}
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("normalize params: %w", err)
	}
	return canonical, nil
}

func validateIntegerRange(raw json.RawMessage, min, max int64, name string) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("params.%s must be an integer", name)
	}
	number, ok := value.(json.Number)
	if !ok {
		return fmt.Errorf("params.%s must be an integer", name)
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || parsed < min || parsed > max {
		return fmt.Errorf("params.%s must be an integer between %d and %d", name, min, max)
	}
	return nil
}

func normalizeActionProposalRequest(req ActionProposalRequest) ActionProposalRequest {
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.ResourceType = strings.ToLower(strings.TrimSpace(req.ResourceType))
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.TargetName = strings.TrimSpace(req.TargetName)
	req.Operation = strings.ToLower(strings.TrimSpace(req.Operation))
	return req
}

func actionRiskForOperation(operation string) string {
	switch operation {
	case "delete_pod", "evict_pod", "cordon", "drain":
		return "R3"
	case "rollout_restart", "scale", "uncordon":
		return "R2"
	default:
		return "R0"
	}
}

// ActionProposalPublicHandler creates a canonical manual Run + Action. It is
// intentionally separate from orchestrator ProxyAI: proposal persistence and
// the identity snapshot belong to query-api, the Action control-plane owner.
func (h *Handler) ActionProposalPublicHandler(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" || auth.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	if h.runDAO == nil || h.actionDAO == nil || h.actionPreflight == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "action_proposal_unavailable"})
		return
	}
	var req ActionProposalRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	req = normalizeActionProposalRequest(req)
	if err := validateActionProposalRequest(req); err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": contract.ErrorCodeValidationFailed, "message": err.Error()})
		return
	}
	cluster, err := (&store.ClusterDAO{}).GetByClusterID(strings.TrimSpace(req.ClusterID))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_CLUSTER"})
		return
	}
	if cluster.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	preflight, err := h.actionPreflight.Resolve(r.Context(), PreflightInput{
		ClusterID: req.ClusterID, ResourceType: req.ResourceType, Namespace: req.Namespace,
		TargetName: req.TargetName, Operation: req.Operation, Params: req.Params,
	})
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": "ACTION_PREFLIGHT_FAILED", "message": err.Error()})
		return
	}
	now := time.Now()
	runID := randomUUID()
	actionID := randomUUID()
	requestID := idempotencyRequestID(auth.TenantID, req.IdempotencyKey)
	intent := fmt.Sprintf("K8s %s %s/%s", req.Operation, req.Namespace, req.TargetName)
	if req.ResourceType == "node" {
		intent = fmt.Sprintf("K8s %s node/%s", req.Operation, req.TargetName)
	}
	created, err := h.runDAO.CreateManualAction(r.Context(), store.AIRun{
		RunID: runID, RequestID: requestID, TenantID: auth.TenantID, Principal: auth.UserID,
		PrincipalType: "user", SessionID: auth.SessionID, ScopeKind: "single_cluster",
		PrimaryClusterID: req.ClusterID, Intent: intent, ActionMode: "manual",
		TargetType: req.ResourceType, TargetResourceID: req.TargetName,
		Status: "awaiting_approval", StateVersion: 0, CreatedAt: now, UpdatedAt: now,
	}, store.AIAction{
		ActionID: actionID, RunID: runID, TenantID: auth.TenantID, ClusterID: req.ClusterID,
		ActionType: "kubernetes", ActionHash: preflight.ActionHash,
		HashSchemaVersion: preflight.HashSchemaVersion, ActionVersion: preflight.ActionVersion,
		ProposedBy: auth.UserID, PolicyVersion: preflight.PolicyVersion,
		PreflightStatus: preflight.PreflightStatus, TargetResourceType: preflight.ResourceType,
		IdempotencyKey: req.IdempotencyKey, ProposedRisk: actionRiskForOperation(req.Operation),
		AuthoritativeRisk: actionRiskForOperation(req.Operation), Status: "proposed", DryRun: false,
		Params: preflight.Params, TargetName: preflight.TargetName, TargetUID: preflight.TargetUID,
		ResourceVersion: preflight.ResourceVersion, Namespace: preflight.Namespace, Operation: preflight.Operation,
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyPayloadMismatch) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "IDEMPOTENCY_KEY_REUSED"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "action_proposal_persist_failed"})
		return
	}
	if !created {
		existingRun, getErr := h.runDAO.GetByTenantRequestID(auth.TenantID, requestID)
		if getErr != nil || existingRun == nil {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "ACTION_ALREADY_EXISTS"})
			return
		}
		actions, listErr := h.actionDAO.ListByRun(existingRun.RunID)
		if listErr != nil || len(actions) == 0 {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "ACTION_ALREADY_EXISTS"})
			return
		}
		a := actions[0]
		respondJSON(w, http.StatusOK, actionProposalProjection(existingRun, a, false))
		return
	}
	respondJSON(w, http.StatusCreated, actionProposalProjection(&store.AIRun{RunID: runID, Status: "awaiting_approval", CreatedAt: now}, store.AIAction{
		ActionID: actionID, RunID: runID, ActionHash: preflight.ActionHash, ActionVersion: preflight.ActionVersion,
		Status: "proposed", ExecutionStatus: "proposed", TargetUID: preflight.TargetUID,
		ResourceVersion: preflight.ResourceVersion, TargetName: preflight.TargetName, Namespace: preflight.Namespace,
		Operation: preflight.Operation, TargetResourceType: preflight.ResourceType,
	}, true))
}

func actionProposalProjection(run *store.AIRun, action store.AIAction, created bool) map[string]interface{} {
	return map[string]interface{}{
		"action_id": action.ActionID, "run_id": run.RunID, "status": action.Status,
		"run_status": run.Status, "created": created, "action_version": action.ActionVersion,
		"action_hash": action.ActionHash, "target_resource_type": action.TargetResourceType,
		"target_name": action.TargetName, "target_uid": action.TargetUID,
		"resource_version": action.ResourceVersion, "namespace": action.Namespace,
		"operation": action.Operation, "params": json.RawMessage(action.Params),
		"execution_status": firstNonEmpty(action.ExecutionStatus, "proposed"),
	}
}
