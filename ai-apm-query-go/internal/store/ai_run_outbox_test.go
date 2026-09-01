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
	mock.ExpectExec("UPDATE ai_run_outbox SET status = 'claimed'.*dispatch_epoch = LAST_INSERT_ID\\(dispatch_epoch \\+ 1\\)").
		WillReturnResult(sqlmock.NewResult(7, 1))
	d := &AIRunOutboxDAO{}
	if err := d.Insert(AIRunOutbox{
		InvocationID: "i", RunID: "r", Status: "pending",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	fence, ok, err := d.Claim("i", "owner-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if fence.OwnerID != "owner-1" || fence.TokenHash == "" || fence.Epoch != 7 {
		t.Fatalf("fence not populated: %+v", fence)
	}
}

func TestAIRunOutboxDeliverWithFence(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'delivered'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunOutboxDAO{}
	fence := NewDispatchFence("owner-1")
	if err := d.Deliver("i", fence); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
}

func TestAIRunOutboxRetryWithFence(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'pending'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunOutboxDAO{}
	fence := NewDispatchFence("owner-1")
	if err := d.Retry("i", fence, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Retry: %v", err)
	}
}

func TestAIRunOutboxScanPending(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	rows := sqlmock.NewRows([]string{"invocation_id", "run_id", "status", "dispatch_count",
		"next_retry_at", "dispatch_owner_id", "dispatch_epoch", "dispatch_token_hash",
		"dispatch_expires_at", "created_at", "updated_at"}).
		AddRow("i", "r", "pending", 0, nil, nil, 0, nil, nil, time.Now(), time.Now())
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
