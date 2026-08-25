package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — control-plane 恢复端点（P10 完整闭环 Plan C）。
//
// orchestrator 重启后调用 recovery snapshot 重建 runtime state，证明"重启后不重复
// Tool/Action"。capability=control_plane.runs.recover。一致性快照：同一 DB 事务内
// 读取 Run + Plan/Step + ToolRun + Action + ControlCommand + last_event_sequence。
// ─────────────────────────────────────────────────────────────────────────────

// InternalControlPlaneRecovery handles GET /internal/v1/control-plane/recovery/snapshot?run_id=。
func (h *Handler) InternalControlPlaneRecovery(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	// P0-3：签名 tenant 绑定到目标 Run（authorizeControlPlaneForRun 加载并校验 tenant）。
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.recover", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	snapshot, err := h.recoverySnapshot(runID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "recovery_snapshot_failed"})
		return
	}
	if snapshot["run"] == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

// recoverySnapshot 在**单一 DB 事务**内读取一致快照（P1-4：非六次独立查询，保证
// Run + Plan/Step + ToolRun + Action + ControlCommand + last_event_sequence 同一快照）。
func (h *Handler) recoverySnapshot(runID string) (map[string]interface{}, error) {
	conn := store.GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	run, err := h.runDAO.GetTx(tx, runID)
	if err != nil {
		return nil, err
	}
	steps, err := h.planDAO.ListByRunTx(tx, runID)
	if err != nil {
		return nil, err
	}
	tools, err := h.toolDAO.ListByRunTx(tx, runID)
	if err != nil {
		return nil, err
	}
	actions, err := h.actionDAO.ListByRunTx(tx, runID)
	if err != nil {
		return nil, err
	}
	hypotheses := []store.AIHypothesis{}
	if h.hypothesisDAO != nil {
		hypotheses, err = h.hypothesisDAO.ListByRunTx(tx, runID)
		if err != nil {
			return nil, err
		}
	}
	cmds, err := h.cmdDAO.ListByRunTx(tx, runID)
	if err != nil {
		return nil, err
	}
	approvals, err := h.approvalDAO.ListByRunTx(tx, runID)
	if err != nil {
		return nil, err
	}
	lastSeq, err := h.eventDAO.LastSequenceTx(tx, runID)
	if err != nil {
		return nil, err
	}
	// A1-04：runtime/lease 元数据（recovery 需知 Run 是否被持锁/retry backoff/epoch）。
	var leaseMeta interface{}
	if h.leaseDAO != nil {
		lm, err := h.leaseDAO.GetRuntimeMetadataTx(tx, runID)
		if err != nil && !errors.Is(err, store.ErrRunNotFound) {
			return nil, err
		}
		if lm != nil {
			leaseMeta = leaseHolderToMap(lm)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	snapshot := map[string]interface{}{
		"run":                 airunToMap(run),
		"lease":               leaseMeta,
		"plan_steps":          planStepsToMaps(steps),
		"tool_runs":           toolRunsToMaps(tools),
		"actions":             actionsToMaps(actions),
		"hypotheses":          hypothesesToMaps(hypotheses),
		"control_commands":    controlCommandsToMaps(cmds),
		"approvals":           approvalsToMaps(approvals),
		"last_event_sequence": lastSeq,
	}
	return snapshot, nil
}

func leaseHolderToMap(l *store.RunLeaseHolder) map[string]interface{} {
	m := map[string]interface{}{
		"run_id": l.RunID, "owner_id": l.OwnerID, "epoch": l.Epoch, "claim_id": l.ClaimID,
		"token_hash": l.TokenHash, "wait_kind": l.WaitKind, "retry_attempt": l.RetryAttempt,
	}
	if !l.ExpiresAt.IsZero() {
		m["expires_at"] = l.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if l.RetryBefore != nil {
		m["retry_not_before"] = l.RetryBefore.UTC().Format(time.RFC3339)
	}
	return m
}

func approvalsToMaps(approvals []store.AIApprovalDecision) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(approvals))
	for _, a := range approvals {
		out = append(out, map[string]interface{}{
			"approval_id": a.ApprovalID, "action_id": a.ActionID, "decision": a.Decision,
			"approver": a.Approver, "reason": a.Reason, "action_hash": a.ActionHash,
			"action_version": a.ActionVersion, "decision_idempotency_key": a.DecisionIdempotencyKey,
		})
	}
	return out
}

func planStepsToMaps(steps []store.AIPlanStep) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(steps))
	for _, s := range steps {
		out = append(out, map[string]interface{}{
			"step_id": s.StepID, "seq": s.Seq, "step_type": s.StepType, "status": s.Status,
			"description": s.Description,
			"depends_on":  s.DependsOn, "attempt": s.Attempt, "outcome": nullableStringValue(s.Outcome),
			"result_ref": nullableStringValue(s.ResultRef),
		})
	}
	return out
}

func toolRunsToMaps(tools []store.AIToolRun) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"tool_run_id": t.ToolRunID, "tool_name": t.ToolName, "status": t.Status,
			"idempotency_key": t.IdempotencyKey, "cluster_id": t.ClusterID,
		})
	}
	return out
}

func actionsToMaps(actions []store.AIAction) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(actions))
	for _, a := range actions {
		out = append(out, map[string]interface{}{
			"action_id": a.ActionID, "action_type": a.ActionType, "action_hash": a.ActionHash,
			"hash_schema_version": a.HashSchemaVersion, "action_version": a.ActionVersion,
			"policy_version": a.PolicyVersion, "preflight_status": a.PreflightStatus,
			"target_resource_type": a.TargetResourceType, "run_id": a.RunID,
			"idempotency_key": a.IdempotencyKey, "status": a.Status,
			"authoritative_risk": a.AuthoritativeRisk, "dry_run": a.DryRun,
			"target_name": a.TargetName, "target_uid": a.TargetUID,
			"resource_version": a.ResourceVersion, "namespace": a.Namespace,
			"operation": a.Operation, "execution_status": a.ExecutionStatus,
		})
	}
	return out
}

func hypothesesToMaps(hypotheses []store.AIHypothesis) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(hypotheses))
	for _, h := range hypotheses {
		out = append(out, map[string]interface{}{
			"hypothesis_id": h.HypothesisID, "content": h.Content,
			"confidence": h.Confidence, "status": h.Status,
			"confirmed_by_evidence": h.ConfirmedByEvidence,
		})
	}
	return out
}

func verificationsToMaps(verifications []store.AIVerification) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(verifications))
	for _, v := range verifications {
		out = append(out, map[string]interface{}{
			"verification_id": v.VerificationID, "run_id": v.RunID, "action_id": v.ActionID,
			"status": v.Status, "before_snapshot": json.RawMessage(v.BeforeSnapshot),
			"after_snapshot": json.RawMessage(v.AfterSnapshot), "checks": json.RawMessage(v.Checks),
			"summary": v.Summary, "payload_hash": v.PayloadHash,
			"observation_window_seconds": v.ObservationWindowSeconds,
			"created_at":                 v.CreatedAt, "updated_at": v.UpdatedAt,
		})
	}
	return out
}

func actionAttemptsToMaps(attempts []store.AIActionAttempt) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, map[string]interface{}{
			"attempt_id": a.AttemptID, "action_id": a.ActionID, "run_id": a.RunID,
			"status": a.Status, "executor_id": a.ExecutorID, "action_hash": a.ActionHash,
			"request_digest_sha256": a.RequestDigestSHA256, "error_code": a.ErrorCode,
			"started_at": a.StartedAt, "finished_at": a.FinishedAt, "created_at": a.CreatedAt,
		})
	}
	return out
}

func controlCommandsToMaps(cmds []store.AIControlCommand) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, map[string]interface{}{
			"command_id": c.CommandID, "operation": c.Operation, "status": c.Status,
			"idempotency_key": c.IdempotencyKey,
		})
	}
	return out
}
