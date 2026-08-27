package store

import (
	"database/sql"
	"errors"
)

type GraphReconcileRun struct {
	ReconcileRunID string
	Source         string
	TenantID       string
	ScopeClusterID string
	Generation     int64
	Status         string
	VerticesSeen   int64
	EdgesSeen      int64
	VerticesStaled int64
	EdgesStaled    int64
	ErrorMessage   string
}

type GraphReconcileRunDAO struct{}

func (d *GraphReconcileRunDAO) List(tenantID string, limit int) ([]GraphReconcileRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT reconcile_run_id, source, tenant_id, scope_cluster_id, generation, status,
    vertices_seen, edges_seen, vertices_staled, edges_staled, error_message, completed_at
    FROM graph_reconcile_runs WHERE tenant_id=? ORDER BY started_at DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GraphReconcileRun, 0, limit)
	for rows.Next() {
		var item GraphReconcileRun
		var message sql.NullString
		var completed sql.NullTime
		if err := rows.Scan(&item.ReconcileRunID, &item.Source, &item.TenantID, &item.ScopeClusterID, &item.Generation, &item.Status, &item.VerticesSeen, &item.EdgesSeen, &item.VerticesStaled, &item.EdgesStaled, &message, &completed); err != nil {
			return nil, err
		}
		item.ErrorMessage = message.String
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *GraphReconcileRunDAO) Start(run GraphReconcileRun) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if run.Status == "" {
		run.Status = "running"
	}
	_, err := conn.Exec(`INSERT INTO graph_reconcile_runs
    (reconcile_run_id, source, tenant_id, scope_cluster_id, generation, status)
    VALUES (?, ?, ?, ?, ?, ?)`, run.ReconcileRunID, run.Source, run.TenantID, run.ScopeClusterID, run.Generation, run.Status)
	return err
}

func (d *GraphReconcileRunDAO) Finish(runID, status, message string, verticesSeen, edgesSeen, verticesStaled, edgesStaled int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_reconcile_runs SET status=?, error_message=?, vertices_seen=?, edges_seen=?,
    vertices_staled=?, edges_staled=?, completed_at=NOW() WHERE reconcile_run_id=?`, status, message, verticesSeen,
		edgesSeen, verticesStaled, edgesStaled, runID)
	return err
}

func (d *GraphReconcileRunDAO) SetGeneration(runID string, generation int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_reconcile_runs SET generation=? WHERE reconcile_run_id=? AND status='running'`, generation, runID)
	return err
}
