package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRuntimeLeaseClaimReturnsDatabaseServerTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	runID := "run-server-time"
	serverNow := time.Date(2026, 9, 1, 1, 2, 3, 456000000, time.UTC)
	expiresAt := serverNow.Add(60 * time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status, lease_owner_id").WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "lease_owner_id", "lease_epoch", "lease_expires_at", "lease_claim_id",
			"lease_token_hash", "runtime_wait_kind", "retry_attempt", "retry_not_before",
		}).AddRow("created", nil, int64(0), nil, nil, nil, "none", int64(0), nil))
	mock.ExpectExec("UPDATE ai_runs SET lease_owner_id").WithArgs(
		"executor-1", int64(1), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(60), runID,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO ai_run_claims").WithArgs(
		runID, sqlmock.AnyArg(), "executor-1", int64(1), sqlmock.AnyArg(), "LIVE_INVOCATION",
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT CURRENT_TIMESTAMP\\(3\\), lease_expires_at").WithArgs("run-server-time", "executor-1", int64(1), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"server_now", "lease_expires_at", "lease_remaining_us"}).
			AddRow(serverNow, expiresAt, int64(60000000)))

	holder, err := (&RuntimeLeaseDAO{}).Claim(runID, "executor-1", 60)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !holder.ServerNow.Equal(serverNow) {
		t.Fatalf("ServerNow = %v, want database time %v", holder.ServerNow, serverNow)
	}
	if !holder.ExpiresAt.Equal(expiresAt) || holder.LeaseRemainingMS != 60000 {
		t.Fatalf("lease timing = expires %v remaining %d, want %v/60000", holder.ExpiresAt, holder.LeaseRemainingMS, expiresAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseClaimExactRetryReturnsDatabaseServerTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	runID := "run-exact-retry"
	ownerID := "executor-1"
	claimID := "claim-1"
	rawToken := "caller-generated-token-00000000000000000000000"
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	serverNow := time.Date(2026, 9, 1, 2, 3, 4, 123000000, time.UTC)
	expiresAt := serverNow.Add(45 * time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status, lease_owner_id").WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "lease_owner_id", "lease_epoch", "lease_expires_at", "lease_claim_id",
			"lease_token_hash", "runtime_wait_kind", "retry_attempt", "retry_not_before",
		}).AddRow("running", ownerID, int64(4), expiresAt, claimID, tokenHash, "none", int64(0), nil))
	mock.ExpectQuery("SELECT lease_expires_at >= CURRENT_TIMESTAMP\\(3\\)").WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(true))
	mock.ExpectQuery("SELECT CURRENT_TIMESTAMP\\(3\\), lease_expires_at").WithArgs(runID, ownerID, int64(4), tokenHash).
		WillReturnRows(sqlmock.NewRows([]string{"server_now", "lease_expires_at", "lease_remaining_us"}).
			AddRow(serverNow, expiresAt, int64(45000000)))
	mock.ExpectRollback()

	holder, err := (&RuntimeLeaseDAO{}).Claim(runID, ownerID, 60, ClaimRequest{
		ClaimID: claimID, LeaseToken: rawToken,
	})
	if err != nil {
		t.Fatalf("Claim() exact retry error = %v", err)
	}
	if !holder.ServerNow.Equal(serverNow) {
		t.Fatalf("exact retry ServerNow = %v, want database time %v", holder.ServerNow, serverNow)
	}
	if holder.Epoch != 4 || holder.LeaseRemainingMS != 45000 {
		t.Fatalf("exact retry holder = %+v, want epoch 4/45000ms", holder)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseClaimRejectsIncompleteCallerFencing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	_, err = (&RuntimeLeaseDAO{}).Claim("run-incomplete-claim", "executor-1", 60, ClaimRequest{
		ClaimID: "claim-only",
	})
	if err == nil || !errors.Is(err, ErrClaimRequestIncomplete) {
		t.Fatalf("Claim() error = %v, want ErrClaimRequestIncomplete", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseClaimRejectsShortCallerToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	_, err = (&RuntimeLeaseDAO{}).Claim("run-short-token", "executor-1", 60, ClaimRequest{
		ClaimID: "claim-1", LeaseToken: "too-short",
	})
	if err == nil || !errors.Is(err, ErrClaimTokenTooShort) {
		t.Fatalf("Claim() error = %v, want ErrClaimTokenTooShort", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseFencingTxLocksCurrentRunRow(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT run_id FROM ai_runs WHERE run_id = \\?.*FOR UPDATE").
		WithArgs("run-1", "owner-1", int64(3), "hash-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("run-1"))
	mock.ExpectRollback()
	tx, err := GetDB().Begin()
	if err != nil {
		t.Fatal(err)
	}

	valid, err := LeaseFencingTx(tx, "run-1", "owner-1", 3, "hash-1")
	if err != nil || !valid {
		t.Fatalf("LeaseFencingTx() = valid %v err %v, want true", valid, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
