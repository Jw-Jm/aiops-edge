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
	if body.ActionID == "" || body.ActionHash == "" || body.IdempotencyKey == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	created, err := h.actionDAO.Create(store.AIAction{
		ActionID: body.ActionID, RunID: runID, TenantID: run.TenantID, ClusterID: run.PrimaryClusterID,
		ActionType: body.ActionType, ActionHash: body.ActionHash, IdempotencyKey: body.IdempotencyKey,
		ProposedRisk: body.ProposedRisk, AuthoritativeRisk: body.AuthoritativeRisk,
		Status: firstNonEmpty(body.Status, "proposed"), DryRun: body.DryRun,
		Params: body.Params,
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
