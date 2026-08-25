package store

import (
	"encoding/json"
	"errors"
	"time"
)

// AIActionReconciliation is the durable observation made after an executor
// response was lost. It is written before the Action execution status changes.
type AIActionReconciliation struct {
	ReconciliationID string
	ActionID         string
	AttemptID        string
	ActionHash       string
	Status           string
	ObservedUID      string
	ObservedVersion  string
	ObservedJSON     json.RawMessage
	ErrorCode        string
	CreatedAt        time.Time
}

type AIActionReconciliationDAO struct{}

func (d *AIActionReconciliationDAO) Create(r AIActionReconciliation) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO ai_action_reconciliations
		(reconciliation_id, action_id, attempt_id, action_hash, status, observed_uid,
		 observed_version, observed_json, error_code, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ReconciliationID, r.ActionID, r.AttemptID, r.ActionHash, r.Status,
		r.ObservedUID, r.ObservedVersion, nullableJSON(r.ObservedJSON), r.ErrorCode,
		firstTime(r.CreatedAt, time.Now()))
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
