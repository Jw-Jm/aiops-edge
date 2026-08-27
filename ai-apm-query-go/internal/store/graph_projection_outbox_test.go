package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGraphProjectionOutboxInsertUsesDeterministicEventIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO graph_projection_outbox")).
		WithArgs("event-1", "tenant-1", "cluster-1", "catalog", "aggregate-1", "aggregate-hash", "upsert_vertex", "entity-1", "", "{}", int64(7), "pending", int64(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = (&GraphProjectionOutboxDAO{}).Insert(GraphProjectionOutbox{
		EventID: "event-1", TenantID: "tenant-1", ClusterID: "cluster-1", AggregateType: "catalog",
		AggregateID: "aggregate-1", AggregateKeySHA256: "aggregate-hash", MutationKind: "upsert_vertex",
		EntityUID: "entity-1", PayloadJSON: "{}", AggregateVersion: 7,
	})
	if err != nil {
		t.Fatalf("Insert returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGraphProjectionOutboxClaimOnlyClaimsPendingOrExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE graph_projection_outbox")).
		WithArgs("worker-1", int64(30), "event-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	claimed, err := (&GraphProjectionOutboxDAO{}).Claim("event-1", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if !claimed {
		t.Fatal("Claim returned false after one row was affected")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
