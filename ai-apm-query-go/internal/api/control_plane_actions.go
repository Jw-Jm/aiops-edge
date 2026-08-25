package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 完整闭环 (Phase D) — control-plane action/approval 持久化端点。
//
// P11 (V9.3) 的 OpsAction/Approval 状态经此 durable 持久化到 ai_actions /
// ai_approval_decisions（生产 MySQL）。真实执行仍 F5 禁止——这里只持久化"审批/执行
// 记录"，不触发任何真实 K8s/OpenStack/SSH 动作。
// ─────────────────────────────────────────────────────────────────────────────

// controlPlaneBodyAction 是 action 持久化请求体。
type controlPlaneBodyAction struct {
	ActionID          string          `json:"action_id"`
	ActionType        string          `json:"action_type"`
	ActionHash        string          `json:"action_hash"`
	IdempotencyKey    string          `json:"idempotency_key"`
	ProposedRisk      string          `json:"proposed_risk"`
	AuthoritativeRisk string          `json:"authoritative_risk"`
	Status            string          `json:"status"`
	DryRun            bool            `json:"dry_run"`
	Params            json.RawMessage `json:"params"`
	// Executor safety identity is persisted with the proposal, not inferred at
	// execution time (TOCTOU protection).
	TargetName      string `json:"target_name"`
	TargetUID       string `json:"target_uid"`
	ResourceVersion string `json:"resource_version"`
	Namespace       string `json:"namespace"`
	Operation       string `json:"operation"`
	ResourceType    string `json:"resource_type"`
}

// controlPlaneBodyApproval 是 approval 持久化请求体。
type controlPlaneBodyApproval struct {
	ApprovalID string `json:"approval_id"`
	ActionID   string `json:"action_id"`
	ActionHash string `json:"action_hash"`
	Decision   string `json:"decision"`
	Approver   string `json:"approver"`
	Reason     string `json:"reason"`
}

type controlPlaneBodyHypothesis struct {
	HypothesisID        string  `json:"hypothesis_id"`
	Content             string  `json:"content"`
	Confidence          float64 `json:"confidence"`
	Status              string  `json:"status"`
	ConfirmedByEvidence bool    `json:"confirmed_by_evidence"`
}

type controlPlaneBodyPlanStep struct {
	StepID      string          `json:"step_id"`
	Seq         int             `json:"seq"`
	StepType    string          `json:"step_type"`
	Status      string          `json:"status"`
	ClusterID   string          `json:"cluster_id"`
	Description string          `json:"description"`
	DependsOn   []string        `json:"depends_on"`
	Parameters  json.RawMessage `json:"parameters"`
}

// internalControlPlaneActionAppend 处理 POST .../runs/{id}/actions。
func (h *Handler) internalControlPlaneActionAppend(w http.ResponseWriter, r *http.Request, runID string) {
	_, run, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyAction
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	// action_hash and target UID/RV are derived by query-api preflight; accepting
	// caller-supplied values here would create an approval/TOCTOU split-brain.
	if body.ActionID == "" || body.IdempotencyKey == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if h.actionPreflight == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "action_preflight_unavailable"})
		return
	}
	preflight, err := h.actionPreflight.Resolve(r.Context(), PreflightInput{
		ClusterID: run.PrimaryClusterID, ResourceType: firstNonEmpty(body.ResourceType, "deployment"),
		Namespace: body.Namespace, TargetName: body.TargetName, Operation: body.Operation,
		Params: body.Params,
	})
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": "ACTION_PREFLIGHT_FAILED", "message": err.Error()})
		return
	}
	created, err := h.actionDAO.Create(store.AIAction{
		ActionID: body.ActionID, RunID: runID, TenantID: run.TenantID, ClusterID: run.PrimaryClusterID,
		ActionType: firstNonEmpty(body.ActionType, "kubernetes"), ActionHash: preflight.ActionHash,
		HashSchemaVersion: preflight.HashSchemaVersion, ActionVersion: preflight.ActionVersion,
		ProposedBy: run.Principal, PolicyVersion: preflight.PolicyVersion,
		PreflightStatus: preflight.PreflightStatus, TargetResourceType: preflight.ResourceType,
		IdempotencyKey: body.IdempotencyKey, ProposedRisk: body.ProposedRisk,
		AuthoritativeRisk: body.AuthoritativeRisk, Status: "proposed", DryRun: preflight.DryRun,
		Params: preflight.Params, TargetName: preflight.TargetName, TargetUID: preflight.TargetUID,
		ResourceVersion: preflight.ResourceVersion, Namespace: preflight.Namespace, Operation: preflight.Operation,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "action_persist_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"action_id": body.ActionID, "created": created})
}

// internalControlPlaneApprovalAppend 处理 POST .../runs/{id}/approvals。
func (h *Handler) internalControlPlaneApprovalAppend(w http.ResponseWriter, r *http.Request, runID string) {
	_, run, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyApproval
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.ApprovalID == "" || body.ActionID == "" || body.Approver == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	now := time.Now()
	created, err := h.approvalDAO.Create(store.AIApprovalDecision{
		ApprovalID: body.ApprovalID, RunID: runID, ActionID: body.ActionID,
		ActionHash: body.ActionHash, TenantID: run.TenantID, ClusterID: run.PrimaryClusterID,
		Decision: firstNonEmpty(body.Decision, "pending"), Approver: body.Approver,
		Reason: body.Reason, DecidedAt: &now,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "approval_persist_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"approval_id": body.ApprovalID, "created": created})
}

// internalControlPlaneHypothesisAppend persists an RCA hypothesis generated
// by the investigation worker.  It is deliberately separate from evidence:
// hypotheses are claims, while evidence remains immutable observation data.
func (h *Handler) internalControlPlaneHypothesisAppend(w http.ResponseWriter, r *http.Request, runID string) {
	_, run, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyHypothesis
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.HypothesisID == "" || body.Content == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	created, err := h.hypothesisDAO.Create(store.AIHypothesis{
		HypothesisID: body.HypothesisID, RunID: runID, TenantID: run.TenantID,
		ClusterID: run.PrimaryClusterID, Content: body.Content, Confidence: body.Confidence,
		Status: firstNonEmpty(body.Status, "proposed"), ConfirmedByEvidence: body.ConfirmedByEvidence,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "hypothesis_persist_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"hypothesis_id": body.HypothesisID, "created": created})
}

func (h *Handler) internalControlPlanePlanStepAppend(w http.ResponseWriter, r *http.Request, runID string) {
	_, run, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyPlanStep
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.StepID == "" || body.StepType == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	clusterID := firstNonEmpty(body.ClusterID, run.PrimaryClusterID)
	created, err := h.planDAO.Create(store.AIPlanStep{
		StepID: body.StepID, RunID: runID, Seq: body.Seq, StepType: body.StepType,
		Status: firstNonEmpty(body.Status, "success"), ClusterID: clusterID,
		Description: body.Description, DependsOn: body.DependsOn, Parameters: body.Parameters,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "plan_step_persist_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"step_id": body.StepID, "created": created})
}
