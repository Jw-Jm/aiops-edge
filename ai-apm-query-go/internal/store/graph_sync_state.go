package store

import (
	"database/sql"
	"errors"
	"time"
)

type GraphSyncState struct {
	Source         string
	TenantID       string
	ScopeClusterID string
	Generation     int64
	Watermark      string
	Status         string
	LastStartedAt  *time.Time
	LastSuccessAt  *time.Time
	LastError      string
	UpdatedAt      time.Time
}

type GraphSyncStateDAO struct{}

func (d *GraphSyncStateDAO) List(tenantID string, limit int) ([]GraphSyncState, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT source, tenant_id, scope_cluster_id, generation, watermark, status,
    last_started_at, last_success_at, last_error, updated_at FROM graph_sync_state
    WHERE tenant_id=? ORDER BY source, scope_cluster_id LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GraphSyncState, 0, limit)
	for rows.Next() {
		var item GraphSyncState
		var started, success sql.NullTime
		if err := rows.Scan(&item.Source, &item.TenantID, &item.ScopeClusterID, &item.Generation, &item.Watermark, &item.Status, &started, &success, &item.LastError, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if started.Valid {
			item.LastStartedAt = &started.Time
		}
		if success.Valid {
			item.LastSuccessAt = &success.Time
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *GraphSyncStateDAO) Upsert(state GraphSyncState) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if state.Status == "" {
		state.Status = "idle"
	}
	_, err := conn.Exec(`INSERT INTO graph_sync_state
    (source, tenant_id, scope_cluster_id, generation, watermark, status, last_error)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE generation=VALUES(generation), watermark=VALUES(watermark),
      status=VALUES(status), last_error=VALUES(last_error), updated_at=NOW()`,
		state.Source, state.TenantID, state.ScopeClusterID, state.Generation, state.Watermark, state.Status, state.LastError)
	return err
}

func (d *GraphSyncStateDAO) Get(source, tenantID, scopeClusterID string) (*GraphSyncState, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var state GraphSyncState
	var startedAt, successAt sql.NullTime
	err := conn.QueryRow(`SELECT source, tenant_id, scope_cluster_id, generation, watermark, status,
    last_started_at, last_success_at, last_error, updated_at FROM graph_sync_state
    WHERE source=? AND tenant_id=? AND scope_cluster_id=?`, source, tenantID, scopeClusterID).
		Scan(&state.Source, &state.TenantID, &state.ScopeClusterID, &state.Generation, &state.Watermark,
			&state.Status, &startedAt, &successAt, &state.LastError, &state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if startedAt.Valid {
		state.LastStartedAt = &startedAt.Time
	}
	if successAt.Valid {
		state.LastSuccessAt = &successAt.Time
	}
	return &state, nil
}

func (d *GraphSyncStateDAO) Start(source, tenantID, scopeClusterID string, generation int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO graph_sync_state (source, tenant_id, scope_cluster_id, generation, status, last_started_at)
    VALUES (?, ?, ?, ?, 'running', NOW()) ON DUPLICATE KEY UPDATE generation=VALUES(generation), status='running', last_started_at=NOW()`,
		source, tenantID, scopeClusterID, generation)
	return err
}

// StartLocked advances a source generation while holding the canonical state
// row lock.  Reconcile callers use this instead of a read-then-write pair so
// generation assignment remains atomic even when a lease is accidentally
// duplicated or a second worker races during recovery.
func (d *GraphSyncStateDAO) StartLocked(source, tenantID, scopeClusterID string) (int64, int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, 0, errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	previous := int64(0)
	var status string
	rowErr := tx.QueryRow(`SELECT generation, status FROM graph_sync_state
    WHERE source=? AND tenant_id=? AND scope_cluster_id=? FOR UPDATE`, source, tenantID, scopeClusterID).Scan(&previous, &status)
	if rowErr != nil && !errors.Is(rowErr, sql.ErrNoRows) {
		return 0, 0, rowErr
	}
	generation := previous + 1
	if errors.Is(rowErr, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO graph_sync_state (source, tenant_id, scope_cluster_id, generation, status, last_started_at)
      VALUES (?, ?, ?, ?, 'running', NOW())`, source, tenantID, scopeClusterID, generation)
	} else {
		_, err = tx.Exec(`UPDATE graph_sync_state SET generation=?, status='running', last_started_at=NOW(), last_error=''
      WHERE source=? AND tenant_id=? AND scope_cluster_id=?`, generation, source, tenantID, scopeClusterID)
	}
	if err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return previous, generation, nil
}

func (d *GraphSyncStateDAO) Finish(source, tenantID, scopeClusterID, watermark string, generation int64, status, lastError string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_sync_state SET watermark=?, generation=?, status=?, last_error=?,
    last_success_at=IF(?='success', NOW(), last_success_at), updated_at=NOW()
    WHERE source=? AND tenant_id=? AND scope_cluster_id=?`, watermark, generation, status, lastError, status,
		source, tenantID, scopeClusterID)
	return err
}
