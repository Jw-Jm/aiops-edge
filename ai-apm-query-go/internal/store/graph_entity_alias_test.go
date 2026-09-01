package store

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestGraphEntityAliasUpsertManyUsesOneAtomicBoundedBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	defer SetDB(previous)

	aliases := []GraphEntityAlias{
		{TenantID: "tenant-1", ScopeClusterID: "cluster-1", Source: "graph-load-test", AliasType: "name", AliasValue: "service-a", CanonicalEntityUID: "loadtest:vertex:000000"},
		{TenantID: "tenant-1", ScopeClusterID: "cluster-1", Source: "graph-load-test", AliasType: "name", AliasValue: "service-b", CanonicalEntityUID: "loadtest:vertex:000001", Confidence: 0.9},
	}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO graph_entity_alias")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectCommit()

	if err := (&GraphEntityAliasDAO{}).UpsertMany(aliases); err != nil {
		t.Fatalf("UpsertMany() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGraphEntityAliasUpsertManyRejectsInvalidAliasBeforeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	defer SetDB(previous)

	err = (&GraphEntityAliasDAO{}).UpsertMany([]GraphEntityAlias{{TenantID: "tenant-1"}})
	if err == nil {
		t.Fatal("UpsertMany() accepted incomplete alias")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGraphEntityAliasUpsertManyRetriesDeadlockAfterRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	defer SetDB(previous)

	alias := []GraphEntityAlias{{TenantID: "tenant-1", ScopeClusterID: "cluster-1", Source: "graph-load-test", AliasType: "name", AliasValue: "service-a", CanonicalEntityUID: "loadtest:vertex:000000"}}
	insert := regexp.QuoteMeta("INSERT INTO graph_entity_alias")
	mock.ExpectBegin()
	mock.ExpectExec(insert).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(&mysqlDriver.MySQLError{Number: 1213, Message: "deadlock"})
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectExec(insert).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := (&GraphEntityAliasDAO{}).UpsertMany(alias); err != nil {
		t.Fatalf("UpsertMany() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
