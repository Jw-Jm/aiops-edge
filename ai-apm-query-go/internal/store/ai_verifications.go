package store

import (
	"encoding/json"
	"errors"
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
}

type AIVerificationDAO struct{}

func (d *AIVerificationDAO) Create(v AIVerification) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	window := v.ObservationWindowSeconds
	if window <= 0 {
		window = 120
	}
	_, err := conn.Exec(`INSERT INTO ai_verifications
		(verification_id, run_id, action_id, tenant_id, cluster_id, status,
		 before_snapshot, after_snapshot, observation_window_seconds, checks_json,
		 summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.VerificationID, v.RunID, v.ActionID, v.TenantID, v.ClusterID,
		firstNonEmptyStr2(v.Status, "inconclusive"), nullableJSON(v.BeforeSnapshot),
		nullableJSON(v.AfterSnapshot), window, nullableJSON(v.Checks), v.Summary,
		firstTime(v.CreatedAt, time.Now()), firstTime(v.UpdatedAt, time.Now()),
	)
	if err != nil {
		if isDuplicateKey(err) {
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
