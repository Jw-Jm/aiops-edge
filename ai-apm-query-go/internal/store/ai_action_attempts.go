package store

import (
	"encoding/json"
	"errors"
	"time"
)

// AIActionAttempt is the durable execution-attempt record. The action itself
// remains immutable; every executor call gets an auditable attempt row.
type AIActionAttempt struct {
	AttemptID           string
	ActionID            string
	RunID               string
	TenantID            string
	ClusterID           string
	IdempotencyKey      string
	ActionHash          string
	RequestDigestSHA256 string
	Status              string
	ExecutorID          string
	RequestJSON         json.RawMessage
	ResultJSON          json.RawMessage
	ErrorCode           string
	StartedAt           *time.Time
	FinishedAt          *time.Time
	CreatedAt           time.Time
}

// AIActionAttemptDAO owns ai_action_attempts. The immutable action/idempotency
// pair is the durable executor boundary; callers must not execute after a
// failed or duplicate insert because that would create an untracked mutation.
type AIActionAttemptDAO struct{}

func (d *AIActionAttemptDAO) Create(a AIActionAttempt) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO ai_action_attempts
		(attempt_id, action_id, run_id, tenant_id, cluster_id, idempotency_key,
		 action_hash, request_digest_sha256, status, executor_id, request_json,
		 result_json, error_code, started_at, finished_at, completed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, a.ActionID, a.RunID, a.TenantID, a.ClusterID, a.IdempotencyKey,
		a.ActionHash, firstNonEmptyStr2(a.RequestDigestSHA256, a.ActionHash),
		firstNonEmptyStr2(a.Status, "running"), a.ExecutorID,
		nullableJSON(a.RequestJSON), nullableJSON(a.ResultJSON), a.ErrorCode,
		a.StartedAt, a.FinishedAt, a.FinishedAt, firstTime(a.CreatedAt, time.Now()),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *AIActionAttemptDAO) Update(attemptID, status string, result []byte, errorCode string, finishedAt *time.Time) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE ai_action_attempts SET status = ?, result_json = ?,
		error_code = ?, finished_at = ?, completed_at = ? WHERE attempt_id = ?`,
		status, nullableJSON(result), errorCode, finishedAt, finishedAt, attemptID)
	return err
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func firstTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}
