package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P0#1/#2/#10：统一 Run Control Service。
//
// Public / Internal / Admin Cancel 全部收敛到 RunControlService.CancelTx：
//   - SELECT Run FOR UPDATE
//   - 校验 expected_version + 非终态（终态不可复活）
//   - set cancelled + state_version++
//   - lease_epoch++ + clear owner/claim/token/expiry/heartbeat（Cancel 原子失效 Lease，旧 executor
//     Renew/Commit/Tool start 全部被 Fence）
//   - append RUN_CANCELLED event
//   - store command exact response（幂等 command_id + payload_hash）
//   - COMMIT
//
// 该事务是 Cancel 的唯一权威；Public/Internal/Admin 只是不同的鉴权入口。
// ─────────────────────────────────────────────────────────────────────────────

// RunControlService 统一 Run 控制面（Cancel 权威事务）。
type RunControlService struct {
	runDAO    *store.AIRunDAO
	cmdDAO    *store.AIControlCommandDAO
	eventDAO  *store.AIRunEventDAO
}

// CancelResult 是 Cancel 的返回值。
type CancelResult struct {
	Cancelled bool
	Run       *store.AIRun
	Error     error
}

// CancelTx 统一 Cancel 权威事务（P0#1）。
// commandID + payloadHash 用于幂等（同 command_id 同 payload → 返回首次响应；不同 → 409）。
func (s *RunControlService) CancelTx(runID, commandID, payloadHash string, expectedVersion int64, requesterID string) CancelResult {
	conn := store.GetDB()
	if conn == nil {
		return CancelResult{Error: errors.New("mysql unavailable")}
	}
	tx, err := conn.Begin()
	if err != nil {
		return CancelResult{Error: err}
	}
	defer tx.Rollback()

	// 0) command 幂等 fast-path（in-tx recheck）
	if commandID != "" {
		if existing, err := s.cmdDAO.GetTx(tx, commandID); err == nil {
			// 同 command_id 已存在：同 payload → 返回首次响应；不同 → 409。
			if existing.PayloadHash != payloadHash {
				return CancelResult{Error: &cancelIdempotencyReusedError{}}
			}
			return CancelResult{Cancelled: true, Error: nil}
		} else if !errors.Is(err, store.ErrCommandNotFound) {
			return CancelResult{Error: err}
		}
	}

	// 1) SELECT Run FOR UPDATE + 非终态校验
	var currentStatus string
	var currentVersion int64
	if err := tx.QueryRow(
		`SELECT status, state_version FROM ai_runs WHERE run_id = ? FOR UPDATE`, runID,
	).Scan(&currentStatus, &currentVersion); err != nil {
		if errors.Is(err, sqlErrNoRows()) {
			return CancelResult{Error: store.ErrRunNotFound}
		}
		return CancelResult{Error: err}
	}
	if store.IsTerminalStatus(currentStatus) {
		return CancelResult{Error: store.ErrRunTerminal}
	}
	if expectedVersion != currentVersion {
		return CancelResult{Error: &cancelVersionConflictError{Expected: expectedVersion, Actual: currentVersion}}
	}

	// 2) 原子 Cancel：status=cancelled + state_version++ + lease_epoch++ + clear lease
	now := time.Now()
	if _, err := tx.Exec(
		`UPDATE ai_runs SET status = 'cancelled', state_version = state_version + 1,
		   updated_at = ?, finished_at = ?, lease_epoch = lease_epoch + 1,
		   lease_owner_id = NULL, lease_claim_id = NULL, lease_token_hash = NULL,
		   lease_expires_at = NULL, heartbeat_at = NULL
		 WHERE run_id = ? AND state_version = ? AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		now, now, runID, expectedVersion,
	); err != nil {
		return CancelResult{Error: err}
	}

	// 3) append RUN_CANCELLED event
	if _, _, err := s.eventDAO.AppendTx(tx, store.AIRunEvent{
		RunID: runID, EventID: newUUID(), EventType: "run.cancelled",
		Payload: json.RawMessage(`{"requester":"` + requesterID + `"}`),
	}); err != nil {
		return CancelResult{Error: err}
	}

	// 4) store command exact response（幂等）
	if commandID != "" {
		resp, _ := json.Marshal(map[string]interface{}{"status": "cancelled", "state_version": currentVersion + 1})
		created, err := s.cmdDAO.CreateTx(tx, store.AIControlCommand{
			CommandID: commandID, RunID: runID, Operation: "cancel",
			PayloadHash: payloadHash, ResponseJSON: resp, Status: "done",
			IdempotencyKey: "cancel:" + runID, CreatedAt: now,
		})
		if err != nil {
			if errors.Is(err, store.ErrCommandDuplicate) {
				// 竞态：另一并发已写同 command → 返回幂等命中。
				return CancelResult{Cancelled: true, Error: nil}
			}
			return CancelResult{Error: err}
		}
		if !created {
			return CancelResult{Cancelled: true, Error: nil}
		}
	}

	if err := tx.Commit(); err != nil {
		return CancelResult{Error: err}
	}
	updated, _ := s.runDAO.Get(runID)
	return CancelResult{Cancelled: true, Run: updated}
}

// --- helpers / errors -----------------------------------------------------

type cancelVersionConflictError struct{ Expected, Actual int64 }

func (e *cancelVersionConflictError) Error() string { return "cancel version conflict" }

type cancelIdempotencyReusedError struct{}

func (e *cancelIdempotencyReusedError) Error() string { return "cancel idempotency key reused" }

func sqlErrNoRows() error { return errors.New("sql: no rows in result set") }

func newUUID() string { return store.NewUUIDv4() }

// respondCancelError 统一 Cancel 错误响应。
func respondCancelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrRunNotFound):
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
	case errors.Is(err, store.ErrRunTerminal):
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
	default:
		var cvc *cancelVersionConflictError
		if errors.As(err, &cvc) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
			return
		}
		var cir *cancelIdempotencyReusedError
		if errors.As(err, &cir) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "IDEMPOTENCY_KEY_REUSED"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "cancel_failed"})
	}
}
