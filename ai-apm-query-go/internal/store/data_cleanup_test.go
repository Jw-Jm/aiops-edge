package store

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func setupDataCleanupDB(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	previous := GetDB()
	SetDB(db)
	return mock, func() {
		db.Close()
		SetDB(previous)
	}
}

func TestDataCleanupDAOCreateAndGetByPreview(t *testing.T) {
	mock, cleanup := setupDataCleanupDB(t)
	defer cleanup()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	op := DataCleanupOperation{
		OperationID:      "op-1",
		PreviewID:        "preview-1",
		TenantID:         "tenant-1",
		UserID:           "user-1",
		RequestDigest:    "digest-1",
		ConfirmationHash: "hash-1",
		IdempotencyKey:   "idem-1",
		CanonicalRequest: []byte(`{"scopes":["alert_events"]}`),
		PlanJSON:         []byte(`{"items":[]}`),
		Status:           "preview",
		ExpiresAt:        now.Add(10 * time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO data_cleanup_operations")).
		WithArgs(op.OperationID, op.PreviewID, op.TenantID, op.UserID, op.IdempotencyKey,
			op.RequestDigest, op.ConfirmationHash, op.CanonicalRequest, op.PlanJSON,
			op.Status, op.ExpiresAt, op.CreatedAt, op.UpdatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := (&DataCleanupDAO{}).Create(op); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rows := sqlmock.NewRows([]string{
		"operation_id", "preview_id", "tenant_id", "user_id", "idempotency_key",
		"request_digest", "confirmation_hash", "canonical_request", "plan_json",
		"result_json", "status", "expires_at", "confirmed_at", "created_at", "updated_at",
	}).AddRow(op.OperationID, op.PreviewID, op.TenantID, op.UserID, op.IdempotencyKey,
		op.RequestDigest, op.ConfirmationHash, op.CanonicalRequest, op.PlanJSON,
		nil, op.Status, op.ExpiresAt, nil, op.CreatedAt, op.UpdatedAt)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT operation_id, preview_id")).
		WithArgs(op.TenantID, op.PreviewID).WillReturnRows(rows)
	got, err := (&DataCleanupDAO{}).GetByPreviewID(op.TenantID, op.PreviewID)
	if err != nil {
		t.Fatalf("GetByPreviewID() error = %v", err)
	}
	if got.OperationID != op.OperationID || got.RequestDigest != op.RequestDigest || got.Status != "preview" {
		t.Fatalf("GetByPreviewID() = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDataCleanupDAOConsumePreviewUsesAtomicExpiryAndDigestGuard(t *testing.T) {
	mock, cleanup := setupDataCleanupDB(t)
	defer cleanup()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	expect := mock.ExpectExec(regexp.QuoteMeta("UPDATE data_cleanup_operations SET status='queued'"))
	expect.WithArgs(now, now, "tenant-1", "preview-1", "digest-1", "hash-1", now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	consumed, err := (&DataCleanupDAO{}).ConsumePreview("tenant-1", "preview-1", "digest-1", "hash-1", now)
	if err != nil {
		t.Fatalf("ConsumePreview() error = %v", err)
	}
	if !consumed {
		t.Fatal("ConsumePreview() = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDataCleanupDAOGetByOperationReturnsNotFoundWithoutCrossTenantLeak(t *testing.T) {
	mock, cleanup := setupDataCleanupDB(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT operation_id, preview_id")).
		WithArgs("tenant-2", "op-1").WillReturnError(sql.ErrNoRows)
	if _, err := (&DataCleanupDAO{}).GetByOperationID("tenant-2", "op-1"); err != sql.ErrNoRows {
		t.Fatalf("GetByOperationID() error = %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDataCleanupDAORecordAudit(t *testing.T) {
	mock, cleanup := setupDataCleanupDB(t)
	defer cleanup()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	detail := []byte(`{"preview_id":"preview-1"}`)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO platform_audit_events")).
		WithArgs("", nil, "tenant-1", nil, "user-1", "query-api", "data_cleanup.preview", "success", detail, now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := (&DataCleanupDAO{}).RecordAudit("tenant-1", "user-1", "data_cleanup.preview", "success", detail, now); err != nil {
		t.Fatalf("RecordAudit() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
