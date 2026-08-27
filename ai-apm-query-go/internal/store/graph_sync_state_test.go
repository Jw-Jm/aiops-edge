package store

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGraphSyncStateUpsertKeepsSourceWatermark(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO graph_sync_state")).
		WithArgs("kubernetes", "tenant-1", "cluster-1", int64(9), "rv-9", "success", "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = (&GraphSyncStateDAO{}).Upsert(GraphSyncState{
		TenantID: "tenant-1", ScopeClusterID: "cluster-1", Source: "kubernetes", Watermark: "rv-9",
		Generation: 9, Status: "success",
	})
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
