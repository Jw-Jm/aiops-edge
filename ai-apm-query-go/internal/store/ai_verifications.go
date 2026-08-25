package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AIVerification is the durable, query-api-owned result of an independent
// read-only observation window.  Status is one of passed, failed, regressed,
// or inconclusive; callers must not infer success from a missing row.
type AIVerification struct {
	VerificationID           string
	RunID                    string
	ActionID                 string
	TenantID                 string
	ClusterID                string
	Status                   string
	BeforeSnapshot           json.RawMessage
	AfterSnapshot            json.RawMessage
	ObservationWindowSeconds int
	Checks                   json.RawMessage
	Summary                  string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	PayloadHash              string
}

type AIVerificationDAO struct{}

func (d *AIVerificationDAO) ListByRun(runID string) ([]AIVerification, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(`SELECT verification_id, run_id, action_id, tenant_id, cluster_id,
		status, before_snapshot, after_snapshot, observation_window_seconds, checks_json,
		summary, payload_hash, created_at, updated_at
		FROM ai_verifications WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AIVerification, 0)
	for rows.Next() {
		var v AIVerification
		if err := rows.Scan(&v.VerificationID, &v.RunID, &v.ActionID, &v.TenantID, &v.ClusterID,
			&v.Status, &v.BeforeSnapshot, &v.AfterSnapshot, &v.ObservationWindowSeconds,
			&v.Checks, &v.Summary, &v.PayloadHash, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *AIVerificationDAO) Create(v AIVerification) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	window := v.ObservationWindowSeconds
	if window <= 0 {
		window = 120
	}
	payloadHash := v.PayloadHash
	if payloadHash == "" {
		payload, _ := json.Marshal(struct {
			RunID  string          `json:"run_id"`
			Action string          `json:"action_id"`
			Status string          `json:"status"`
			Window int             `json:"window_seconds"`
			Before json.RawMessage `json:"before"`
			After  json.RawMessage `json:"after"`
			Checks json.RawMessage `json:"checks"`
		}{v.RunID, v.ActionID, v.Status, window, v.BeforeSnapshot, v.AfterSnapshot, v.Checks})
		hash := sha256.Sum256(payload)
		payloadHash = fmt.Sprintf("%x", hash[:])
	}
	_, err := conn.Exec(`INSERT INTO ai_verifications
		(verification_id, run_id, action_id, tenant_id, cluster_id, status,
		 before_snapshot, after_snapshot, observation_window_seconds, checks_json,
		 summary, payload_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.VerificationID, v.RunID, v.ActionID, v.TenantID, v.ClusterID,
		firstNonEmptyStr2(v.Status, "inconclusive"), nullableJSON(v.BeforeSnapshot),
		nullableJSON(v.AfterSnapshot), window, nullableJSON(v.Checks), v.Summary, payloadHash,
		firstTime(v.CreatedAt, time.Now()), firstTime(v.UpdatedAt, time.Now()),
	)
	if err != nil {
		if isDuplicateKey(err) {
			var existingHash string
			if lookupErr := conn.QueryRow(`SELECT payload_hash FROM ai_verifications WHERE verification_id = ?`, v.VerificationID).Scan(&existingHash); lookupErr != nil {
				return false, lookupErr
			}
			if existingHash != payloadHash {
				return false, ErrIdempotencyPayloadMismatch
			}
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *AIVerificationDAO) UpdateStatus(verificationID, status string,
	afterSnapshot, checks []byte, summary string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE ai_verifications SET status = ?, after_snapshot = ?,
		checks_json = ?, summary = ?, updated_at = ? WHERE verification_id = ?`,
		status, nullableJSON(afterSnapshot), nullableJSON(checks), summary,
		time.Now(), verificationID)
	return err
}
