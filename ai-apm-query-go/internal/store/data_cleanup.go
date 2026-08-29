package store

import (
	"database/sql"
	"errors"
	"time"
)

// DataCleanupOperation is the durable state for one preview/execute lifecycle.
// ConfirmationHash is persisted instead of the one-time confirmation token.
type DataCleanupOperation struct {
	OperationID      string
	PreviewID        string
	TenantID         string
	UserID           string
	RequestDigest    string
	ConfirmationHash string
	IdempotencyKey   string
	CanonicalRequest []byte
	PlanJSON         []byte
	ResultJSON       []byte
	Status           string
	ExpiresAt        time.Time
	ConfirmedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DataCleanupDAO persists the preview and operation state in query-api's MySQL
// control plane. All reads include tenant_id to prevent cross-tenant disclosure.
type DataCleanupDAO struct{}

const dataCleanupSelect = `SELECT operation_id, preview_id, tenant_id, user_id,
 idempotency_key, request_digest, confirmation_hash, canonical_request, plan_json,
 result_json, status, expires_at, confirmed_at, created_at, updated_at
 FROM data_cleanup_operations`

func (d *DataCleanupDAO) Create(operation DataCleanupOperation) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO data_cleanup_operations
 (operation_id, preview_id, tenant_id, user_id, idempotency_key, request_digest,
  confirmation_hash, canonical_request, plan_json, status, expires_at, created_at, updated_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.OperationID, operation.PreviewID, operation.TenantID, operation.UserID,
		operation.IdempotencyKey, operation.RequestDigest, operation.ConfirmationHash,
		operation.CanonicalRequest, operation.PlanJSON, operation.Status, operation.ExpiresAt,
		operation.CreatedAt, operation.UpdatedAt)
	return err
}

// RecordAudit writes the cleanup lifecycle event to the platform audit SoT.
// Audit failures are surfaced to the caller so the API can log them without
// turning a completed data mutation into a false failure.
func (d *DataCleanupDAO) RecordAudit(tenantID, userID, action, result string, detail []byte, now time.Time) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO platform_audit_events
 (request_id, run_id, tenant_id, cluster_id, user_id, service_identity, action, result, detail, created_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"", nil, tenantID, nil, userID, "query-api", action, result, detail, now)
	return err
}

func (d *DataCleanupDAO) GetByPreviewID(tenantID, previewID string) (*DataCleanupOperation, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	return scanDataCleanupOperation(conn.QueryRow(dataCleanupSelect+` WHERE tenant_id=? AND preview_id=?`, tenantID, previewID))
}

func (d *DataCleanupDAO) GetByOperationID(tenantID, operationID string) (*DataCleanupOperation, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	return scanDataCleanupOperation(conn.QueryRow(dataCleanupSelect+` WHERE tenant_id=? AND operation_id=?`, tenantID, operationID))
}

// ConsumePreview atomically validates and consumes a still-valid preview. The
// affected-row count is the concurrency guard for one-time confirmation.
func (d *DataCleanupDAO) ConsumePreview(tenantID, previewID, requestDigest, confirmationHash string, now time.Time) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	result, err := conn.Exec(`UPDATE data_cleanup_operations SET status='queued', confirmed_at=?, updated_at=?
 WHERE tenant_id=? AND preview_id=? AND request_digest=? AND confirmation_hash=?
   AND status='preview' AND expires_at>?`,
		now, now, tenantID, previewID, requestDigest, confirmationHash, now)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *DataCleanupDAO) MarkRunning(tenantID, operationID string, now time.Time) (bool, error) {
	return d.updateStatus(tenantID, operationID, "queued", "running", now, nil)
}

func (d *DataCleanupDAO) Finish(tenantID, operationID, status string, resultJSON []byte, now time.Time) (bool, error) {
	return d.updateStatus(tenantID, operationID, "running", status, now, resultJSON)
}

func (d *DataCleanupDAO) updateStatus(tenantID, operationID, from, to string, now time.Time, resultJSON []byte) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	var (
		result sql.Result
		err    error
	)
	if resultJSON == nil {
		result, err = conn.Exec(`UPDATE data_cleanup_operations SET status=?, updated_at=?
 WHERE tenant_id=? AND operation_id=? AND status=?`, to, now, tenantID, operationID, from)
	} else {
		result, err = conn.Exec(`UPDATE data_cleanup_operations SET status=?, result_json=?, updated_at=?
 WHERE tenant_id=? AND operation_id=? AND status=?`, to, resultJSON, now, tenantID, operationID, from)
	}
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

type dataCleanupScanner interface {
	Scan(dest ...any) error
}

func scanDataCleanupOperation(row dataCleanupScanner) (*DataCleanupOperation, error) {
	var operation DataCleanupOperation
	var confirmedAt sql.NullTime
	if err := row.Scan(
		&operation.OperationID, &operation.PreviewID, &operation.TenantID, &operation.UserID,
		&operation.IdempotencyKey, &operation.RequestDigest, &operation.ConfirmationHash,
		&operation.CanonicalRequest, &operation.PlanJSON, &operation.ResultJSON,
		&operation.Status, &operation.ExpiresAt, &confirmedAt,
		&operation.CreatedAt, &operation.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if confirmedAt.Valid {
		operation.ConfirmedAt = &confirmedAt.Time
	}
	return &operation, nil
}
