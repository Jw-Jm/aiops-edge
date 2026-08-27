package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

type GraphShadowDiffRun struct {
	DiffRunID      string
	TenantID       string
	ScopeClusterID string
	SampleKind     string
	SampleCount    int
	MismatchCount  int
	Detail         interface{}
}

type GraphShadowDiffDAO struct{}

func (d *GraphShadowDiffDAO) List(tenantID string, limit int) ([]GraphShadowDiffRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT diff_run_id, tenant_id, scope_cluster_id, sample_kind, sample_count, mismatch_count, detail_json
    FROM graph_shadow_diff_runs WHERE tenant_id=? ORDER BY created_at DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GraphShadowDiffRun, 0, limit)
	for rows.Next() {
		var item GraphShadowDiffRun
		var clusterID, detail sql.NullString
		if err := rows.Scan(&item.DiffRunID, &item.TenantID, &clusterID, &item.SampleKind, &item.SampleCount, &item.MismatchCount, &detail); err != nil {
			return nil, err
		}
		item.ScopeClusterID = clusterID.String
		if detail.Valid {
			var value interface{}
			if json.Unmarshal([]byte(detail.String), &value) == nil {
				item.Detail = value
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *GraphShadowDiffDAO) Insert(diff GraphShadowDiffRun) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	detail := []byte("null")
	if diff.Detail != nil {
		encoded, err := json.Marshal(diff.Detail)
		if err != nil {
			return err
		}
		detail = encoded
	}
	_, err := conn.Exec(`INSERT INTO graph_shadow_diff_runs
    (diff_run_id, tenant_id, scope_cluster_id, sample_kind, sample_count, mismatch_count, detail_json)
    VALUES (?, ?, ?, ?, ?, ?, ?)`, diff.DiffRunID, diff.TenantID, diff.ScopeClusterID, diff.SampleKind,
		diff.SampleCount, diff.MismatchCount, detail)
	return err
}
