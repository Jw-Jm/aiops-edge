package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAIRunOutboxInsertAndClaim(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'claimed'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunOutboxDAO{}
	if err := d.Insert(AIRunOutbox{
		InvocationID: "i", RunID: "r", Status: "pending",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	ok, err := d.Claim("i", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
}

func TestAIRunOutboxDeliver(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'delivered'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunOutboxDAO{}
	if err := d.Deliver("i"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
}

func TestAIRunOutboxScanPending(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	rows := sqlmock.NewRows([]string{"invocation_id", "run_id", "status", "dispatch_count",
		"next_retry_at", "created_at", "updated_at"}).
		AddRow("i", "r", "pending", 0, nil, time.Now(), time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT invocation_id, run_id")).
		WillReturnRows(rows)
	d := &AIRunOutboxDAO{}
	list, err := d.ScanPending(10)
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(list) != 1 || list[0].InvocationID != "i" {
		t.Fatalf("got %+v", list)
	}
}
