package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type ActionDecisionRequest struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
	ActionVersion  int64  `json:"action_version"`
}

type ActionDecisionResult struct {
	ApprovalID    string `json:"approval_id"`
	ActionID      string `json:"action_id"`
	ActionVersion int64  `json:"action_version"`
	Decision      string `json:"decision"`
	RunStatus     string `json:"run_status"`
	CommandID     string `json:"command_id,omitempty"`
	Replay        bool   `json:"replay,omitempty"`
}

func validateActionDecision(req ActionDecisionRequest) error {
	req.Decision = strings.ToLower(strings.TrimSpace(req.Decision))
	if req.Decision != "approved" && req.Decision != "rejected" {
		return errors.New("decision must be approved or rejected")
	}
	if req.ActionVersion < 1 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return errors.New("action_version and idempotency_key are required")
	}
	if req.Decision == "rejected" && strings.TrimSpace(req.Reason) == "" {
		return errors.New("reason is required for rejection")
	}
	return nil
}

type actionDecisionRow struct {
	actionID          string
	runID             string
	tenantID          string
	clusterID         string
	actionHash        string
	hashSchemaVersion int
	actionVersion     int64
	proposedBy        sql.NullString
	preflightStatus   string
	dryRun            int
	status            string
	targetName        string
	targetUID         string
	resourceVersion   string
	namespace         string
	operation         string
	params            []byte
}

type actionDecisionRunRow struct {
	runID          string
	tenantID       string
	principal      string
	status         string
	stateVersion   int64
	primaryCluster string
}

func (h *Handler) decideActionPublic(w http.ResponseWriter, r *http.Request, actionID string) {
	authCtx, ok := requestAuthorizationContext(r)
	if !ok || authCtx.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	var req ActionDecisionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if err := validateActionDecision(req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed, "message": err.Error()})
		return
	}
	result, err := h.decideAction(r.Context(), actionID, authCtx, req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		code := "ACTION_DECISION_REJECTED"
		switch {
		case errors.Is(err, errActionDecisionConflict):
			status, code = http.StatusConflict, "ACTION_DECISION_CONFLICT"
		case errors.Is(err, errActionDecisionIdempotency):
			status, code = http.StatusConflict, "IDEMPOTENCY_KEY_REUSED"
		case errors.Is(err, errActionDecisionUnavailable):
			status, code = http.StatusServiceUnavailable, "PERSISTENCE_UNAVAILABLE"
		}
		respondJSON(w, status, map[string]interface{}{"error": code, "message": err.Error()})
		return
	}
	status := http.StatusAccepted
	if result.Replay {
		status = http.StatusOK
	}
	respondJSON(w, status, result)
}

var (
	errActionDecisionConflict    = errors.New("action decision state conflict")
	errActionDecisionIdempotency = errors.New("action decision idempotency key reused")
	errActionDecisionUnavailable = errors.New("action decision persistence unavailable")
)

func (h *Handler) decideAction(ctx context.Context, actionID string, auth AuthorizationContext, req ActionDecisionRequest) (ActionDecisionResult, error) {
	req.Decision = strings.ToLower(strings.TrimSpace(req.Decision))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.Reason = strings.TrimSpace(req.Reason)
	db := store.GetDB()
	if db == nil {
		return ActionDecisionResult{}, errActionDecisionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ActionDecisionResult{}, fmt.Errorf("%w: begin: %v", errActionDecisionUnavailable, err)
	}
	defer tx.Rollback()

	var existing ActionDecisionResult
	var existingReason, existingApprover string
	var existingVersion int64
	var existingDecision string
	err = tx.QueryRowContext(ctx, `SELECT approval_id, action_id, COALESCE(action_version, 0), decision,
		approver, COALESCE(reason, '') FROM ai_approval_decisions
		WHERE action_id = ? AND decision_idempotency_key = ? LIMIT 1`, actionID, req.IdempotencyKey).
		Scan(&existing.ApprovalID, &existing.ActionID, &existingVersion, &existingDecision, &existingApprover, &existingReason)
	if err == nil {
		if existingApprover != auth.UserID || existingVersion != req.ActionVersion || existingDecision != req.Decision || existingReason != req.Reason {
			return ActionDecisionResult{}, errActionDecisionIdempotency
		}
		existing.Decision = existingDecision
		existing.ActionVersion = existingVersion
		existing.Replay = true
		_ = tx.QueryRowContext(ctx, `SELECT COALESCE(r.status, '') FROM ai_actions a
			LEFT JOIN ai_runs r ON r.run_id = a.run_id WHERE a.action_id = ?`, actionID).Scan(&existing.RunStatus)
		if existing.Decision == "approved" {
			_ = tx.QueryRowContext(ctx, `SELECT command_id FROM ai_action_outbox WHERE action_id = ? AND action_version = ? LIMIT 1`, actionID, existingVersion).Scan(&existing.CommandID)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ActionDecisionResult{}, fmt.Errorf("%w: decision lookup: %v", errActionDecisionUnavailable, err)
	}

	var action actionDecisionRow
	err = tx.QueryRowContext(ctx, `SELECT action_id, run_id, tenant_id, cluster_id, action_hash,
		hash_schema_version, action_version, proposed_by, preflight_status, dry_run, status,
		target_name, target_uid, resource_version, namespace, operation, params_json
		FROM ai_actions WHERE action_id = ? FOR UPDATE`, actionID).Scan(
		&action.actionID, &action.runID, &action.tenantID, &action.clusterID, &action.actionHash,
		&action.hashSchemaVersion, &action.actionVersion, &action.proposedBy, &action.preflightStatus,
		&action.dryRun, &action.status, &action.targetName, &action.targetUID, &action.resourceVersion,
		&action.namespace, &action.operation, &action.params)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionDecisionResult{}, errActionDecisionConflict
	}
	if err != nil {
		return ActionDecisionResult{}, fmt.Errorf("%w: action lookup: %v", errActionDecisionUnavailable, err)
	}
	if action.tenantID != auth.TenantID || action.actionVersion != req.ActionVersion || action.hashSchemaVersion != 2 ||
		action.preflightStatus != "passed" || action.dryRun != 0 || action.status != "proposed" ||
		action.targetUID == "" || action.resourceVersion == "" {
		return ActionDecisionResult{}, errActionDecisionConflict
	}
	canonicalHash, hashErr := contract.CanonicalActionHash(contract.CanonicalActionPayloadV2{
		Version: 1, ActionType: "kubernetes", ResourceType: "deployment",
		Namespace: action.namespace, TargetName: action.targetName,
		TargetUID: action.targetUID, ResourceVersion: action.resourceVersion,
		Operation: action.operation, Params: action.params, PolicyVersion: "action-policy-v1",
	})
	if hashErr != nil || canonicalHash != action.actionHash {
		return ActionDecisionResult{}, errActionDecisionConflict
	}
	if action.proposedBy.Valid && action.proposedBy.String == auth.UserID {
		return ActionDecisionResult{}, errActionDecisionConflict
	}

	var run actionDecisionRunRow
	err = tx.QueryRowContext(ctx, `SELECT run_id, tenant_id, principal, status, state_version,
		COALESCE(primary_cluster_id, '') FROM ai_runs WHERE run_id = ? FOR UPDATE`, action.runID).
		Scan(&run.runID, &run.tenantID, &run.principal, &run.status, &run.stateVersion, &run.primaryCluster)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActionDecisionResult{}, errActionDecisionConflict
		}
		return ActionDecisionResult{}, fmt.Errorf("%w: run lookup: %v", errActionDecisionUnavailable, err)
	}
	if run.tenantID != auth.TenantID || run.tenantID != action.tenantID || run.primaryCluster != action.clusterID || run.status != "awaiting_approval" {
		return ActionDecisionResult{}, errActionDecisionConflict
	}

	now := time.Now()
	approvalID := randomUUID()
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_approval_decisions
		(approval_id, run_id, action_id, action_hash, action_version, decision_idempotency_key,
		tenant_id, cluster_id, decision, approver, reason, decided_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approvalID, action.runID, action.actionID, action.actionHash, action.actionVersion,
		req.IdempotencyKey, action.tenantID, action.clusterID, decision, auth.UserID,
		nullableString(req.Reason), now, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return ActionDecisionResult{}, errActionDecisionIdempotency
		}
		return ActionDecisionResult{}, fmt.Errorf("%w: insert decision: %v", errActionDecisionUnavailable, err)
	}

	newRunStatus := "cancelled"
	newActionStatus := "rejected"
	newExecutionStatus := "rejected"
	commandID := ""
	if decision == "approved" {
		newRunStatus = "executing"
		newActionStatus = "approved"
		newExecutionStatus = "queued"
		commandID = randomUUID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_action_outbox
			(command_id, action_id, action_version, action_hash, run_id, tenant_id, cluster_id,
			status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
			commandID, action.actionID, action.actionVersion, action.actionHash, action.runID,
			action.tenantID, action.clusterID, now, now); err != nil {
			return ActionDecisionResult{}, fmt.Errorf("%w: insert action command: %v", errActionDecisionUnavailable, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_actions SET status = ?, execution_status = ?, updated_at = ? WHERE action_id = ? AND status = 'proposed'`,
		newActionStatus, newExecutionStatus, now, action.actionID); err != nil {
		return ActionDecisionResult{}, fmt.Errorf("%w: update action: %v", errActionDecisionUnavailable, err)
	}
	finished := interface{}(nil)
	if newRunStatus == "cancelled" {
		finished = now
	}
	res, err := tx.ExecContext(ctx, `UPDATE ai_runs SET status = ?, state_version = state_version + 1,
		updated_at = ?, finished_at = ? WHERE run_id = ? AND status = 'awaiting_approval' AND state_version = ?`,
		newRunStatus, now, finished, action.runID, run.stateVersion)
	if err != nil {
		return ActionDecisionResult{}, fmt.Errorf("%w: update run: %v", errActionDecisionUnavailable, err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return ActionDecisionResult{}, errActionDecisionConflict
	}
	if err := tx.Commit(); err != nil {
		return ActionDecisionResult{}, fmt.Errorf("%w: commit: %v", errActionDecisionUnavailable, err)
	}
	return ActionDecisionResult{ApprovalID: approvalID, ActionID: action.actionID,
		ActionVersion: action.actionVersion, Decision: decision, RunStatus: newRunStatus,
		CommandID: commandID}, nil
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
