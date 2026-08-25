package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	actionDispatchPollInterval = time.Second
	actionDispatchLease        = 30 * time.Second
	actionDispatchBatchSize    = 50
)

func actionDispatchOwnerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("action:%s:%d", host, os.Getpid())
}

// RunActionDispatchLoop is enabled only in the query-api dispatch role. It
// never runs in the executor or orchestrator, keeping mutation dispatch under
// the query-api's durable Action authority.
func (h *Handler) RunActionDispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if h.actionOutboxDAO != nil && h.actionDAO != nil {
			h.dispatchActionPending()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(actionDispatchPollInterval):
		}
	}
}

func (h *Handler) dispatchActionPending() {
	rows, err := h.actionOutboxDAO.ScanPending(actionDispatchBatchSize)
	if err != nil {
		return
	}
	var oldestAge time.Duration
	if len(rows) > 0 && !rows[0].CreatedAt.IsZero() {
		oldestAge = time.Since(rows[0].CreatedAt)
	}
	cp.setActionQueue(len(rows), oldestAge)
	for _, row := range rows {
		h.dispatchActionOne(row)
	}
}

func (h *Handler) dispatchActionOne(row store.AIActionOutbox) {
	cp.inc("action_outbox_dispatch")
	fence, claimed, err := h.actionOutboxDAO.Claim(row.CommandID, actionDispatchOwnerID(), actionDispatchLease)
	if err != nil || !claimed {
		return
	}
	retry := func() {
		_ = h.actionOutboxDAO.Retry(row.CommandID, fence, time.Now().Add(backoff(row.DispatchCount)))
	}
	action, err := h.actionDAO.GetByID(row.ActionID)
	if err != nil || action == nil {
		retry()
		return
	}
	// The outbox is an immutable copy of the approved command. A mismatch is a
	// persistence integrity failure, never a reason to execute the row.
	if action.ActionHash != row.ActionHash || action.TenantID != row.TenantID ||
		action.ClusterID != row.ClusterID || action.Status != "approved" || action.DryRun {
		_ = h.actionDAO.UpdateByIdemKey(store.AIAction{RunID: action.RunID, IdempotencyKey: action.IdempotencyKey,
			Status: "rejected", Result: []byte(`{"error":"ACTION_OUTBOX_INTEGRITY_FAILURE"}`)})
		_ = h.actionOutboxDAO.Deliver(row.CommandID, fence)
		return
	}
	approval, err := h.approvalDAO.GetApprovedApproval(action.ActionID)
	if err != nil || approval == nil || approval.ActionHash != row.ActionHash {
		retry()
		return
	}
	var result contract.ActionResult
	if action.ExecutionStatus == "execution_unknown" {
		if h.actionDispatchReconcile != nil {
			result, err = h.actionDispatchReconcile(action)
		} else {
			result, err = h.reconcileAction(action)
		}
	} else {
		if h.actionDispatchExecute != nil {
			result, err = h.actionDispatchExecute(action, approval)
		} else {
			result, err = h.executeApprovedAction(action, approval)
		}
	}
	if err != nil {
		retry()
		return
	}
	if result.Status == "execution_unknown" || result.Status == "reconcile_required" || result.Status == "unknown" {
		cp.inc("action_outbox_unknown")
		retry()
		return
	}
	if result.Status == "success" || result.Status == "failed" || result.Status == "rejected" || result.Status == "rollback_required" {
		h.finalizeActionRun(action, result.Status)
		_ = h.actionOutboxDAO.Deliver(row.CommandID, fence)
		return
	}
	retry()
}

func (h *Handler) reconcileAction(action *store.AIAction) (contract.ActionResult, error) {
	client := currentActionExecutor()
	if client == nil {
		return contract.ActionResult{}, fmt.Errorf("executor client not configured")
	}
	targetSpec := json.RawMessage(action.Params)
	if !json.Valid(targetSpec) {
		return contract.ActionResult{ActionID: action.ActionID, Status: "unknown"}, fmt.Errorf("action params are not valid JSON")
	}
	result, reached, err := client.Reconcile(contract.ActionExecutionContext{
		ActionID: action.ActionID, ActionHash: action.ActionHash, TargetUID: action.TargetUID,
		TargetName: action.TargetName, ResourceVersion: action.ResourceVersion,
		ClusterID: action.ClusterID, Namespace: action.Namespace, Operation: action.Operation,
		TargetSpec: targetSpec,
	})
	if err != nil {
		return result, err
	}
	if !reached {
		return result, fmt.Errorf("executor reconciliation unavailable")
	}
	reconcileStatus := normalizeReconcileOutcomeStatus(result.Status)
	result.Status = normalizeReconcileActionStatus(reconcileStatus)
	observedJSON, _ := json.Marshal(result)
	if h.reconciliationDAO != nil {
		if _, err := h.reconciliationDAO.Create(store.AIActionReconciliation{
			ReconciliationID: deterministicActionReconciliationID(action.ActionID, actionVersion(action)),
			AttemptID:        deterministicActionAttemptID(action.ActionID, actionVersion(action)), ActionID: action.ActionID,
			ActionHash: action.ActionHash, Status: reconcileStatus, ObservedUID: result.ObservedUID,
			ObservedVersion: result.ObservedVersion, ObservedJSON: observedJSON, CreatedAt: time.Now(),
		}); err != nil {
			return result, fmt.Errorf("reconciliation persistence failed: %w", err)
		}
	}
	if result.Status != "execution_unknown" {
		if h.actionDAO != nil {
			if err := h.actionDAO.UpdateExecution(action.ActionID, result.Status, observedJSON, ""); err != nil {
				return result, fmt.Errorf("reconciled action persistence failed: %w", err)
			}
		}
		if h.attemptDAO != nil {
			_ = h.attemptDAO.Update(deterministicActionAttemptID(action.ActionID, actionVersion(action)), result.Status, observedJSON, "", nil)
		}
	}
	return result, nil
}

func normalizeReconcileOutcomeStatus(status string) string {
	switch status {
	case "applied", "success":
		return "applied"
	case "not_applied", "failed", "reconcile_required":
		return "not_applied"
	case "drift", "rejected":
		return "drift"
	default:
		return "unknown"
	}
}

func (h *Handler) finalizeActionRun(action *store.AIAction, executionStatus string) {
	conn := store.GetDB()
	if conn == nil {
		return
	}
	status := "failed"
	if executionStatus == "success" {
		// Executor success proves only that the mutation boundary returned a
		// successful result. Independent Verification owns the terminal Run
		// decision, so successful execution always enters verifying.
		status = "verifying"
	} else if executionStatus == "rollback_required" {
		status = "regressed"
	}
	finishedAt := interface{}(nil)
	if status != "verifying" {
		finishedAt = time.Now()
	}
	_, _ = conn.Exec(`UPDATE ai_runs SET status = ?, state_version = state_version + 1,
		updated_at = NOW(), finished_at = ? WHERE run_id = ? AND status = 'executing'`, status, finishedAt, action.RunID)
	if h.eventDAO != nil {
		payload, _ := json.Marshal(map[string]interface{}{"action_id": action.ActionID, "status": executionStatus, "run_status": status})
		_, _, _ = h.eventDAO.Append(store.AIRunEvent{
			EventID: deterministicActionEventID(action.ActionID, executionStatus), RunID: action.RunID,
			EventType: "action.execution.completed", Payload: payload,
		})
	}
}

func deterministicActionReconciliationID(actionID string, version int64) string {
	return deterministicActionAttemptID("reconcile:"+actionID, version)
}

func deterministicActionEventID(actionID, status string) string {
	return deterministicActionAttemptID("event:"+actionID+":"+status, 1)
}
