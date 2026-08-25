package store

import (
	"database/sql"
	"errors"
	"time"
)

// AIHypothesis is the durable RCA hypothesis record for a Run.
type AIHypothesis struct {
	HypothesisID        string
	RunID               string
	TenantID            string
	ClusterID           string
	Content             string
	Confidence          float64
	Status              string
	ConfirmedByEvidence bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AIHypothesisDAO struct{}

func (d *AIHypothesisDAO) Create(h AIHypothesis) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	confirmed := 0
	if h.ConfirmedByEvidence {
		confirmed = 1
	}
	_, err := conn.Exec(
		`INSERT INTO ai_hypotheses (hypothesis_id, run_id, tenant_id, cluster_id, content,
		   confidence, status, confirmed_by_evidence, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.HypothesisID, h.RunID, h.TenantID, h.ClusterID, h.Content, h.Confidence,
		firstNonEmptyStr2(h.Status, "proposed"), confirmed, time.Now(), time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *AIHypothesisDAO) ListByRun(runID string) ([]AIHypothesis, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT hypothesis_id, run_id, tenant_id, cluster_id, content, confidence,
		   status, confirmed_by_evidence, created_at, updated_at
		 FROM ai_hypotheses WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIHypothesis{}
	for rows.Next() {
		var h AIHypothesis
		var confirmed int
		if err := rows.Scan(&h.HypothesisID, &h.RunID, &h.TenantID, &h.ClusterID, &h.Content,
			&h.Confidence, &h.Status, &confirmed, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.ConfirmedByEvidence = confirmed != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func (d *AIHypothesisDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIHypothesis, error) {
	rows, err := tx.Query(
		`SELECT hypothesis_id, run_id, tenant_id, cluster_id, content, confidence,
		   status, confirmed_by_evidence, created_at, updated_at
		 FROM ai_hypotheses WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIHypothesis{}
	for rows.Next() {
		var h AIHypothesis
		var confirmed int
		if err := rows.Scan(&h.HypothesisID, &h.RunID, &h.TenantID, &h.ClusterID, &h.Content,
			&h.Confidence, &h.Status, &confirmed, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.ConfirmedByEvidence = confirmed != 0
		out = append(out, h)
	}
	return out, rows.Err()
}
