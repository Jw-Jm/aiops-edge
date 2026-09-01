package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAIActionOutboxClaimUsesFencing(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec("UPDATE ai_action_outbox.*dispatch_epoch = LAST_INSERT_ID\\(dispatch_epoch \\+ 1\\)").
		WithArgs("worker-1", sqlmock.AnyArg(), int64(30), int64(30), "cmd-1").
		WillReturnResult(sqlmock.NewResult(9, 1))
	fence, claimed, err := (&AIActionOutboxDAO{}).Claim("cmd-1", "worker-1", 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	if fence.OwnerID != "worker-1" || fence.Epoch != 9 || fence.TokenHash == "" {
		t.Fatalf("invalid fence: %+v", fence)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAIActionOutboxDeliverIsFenced(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_action_outbox SET status = 'delivered'")).
		WithArgs("cmd-1", "worker-1", int64(7), "token-hash").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := (&AIActionOutboxDAO{}).Deliver("cmd-1", DispatchFence{OwnerID: "worker-1", Epoch: 7, TokenHash: "token-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
