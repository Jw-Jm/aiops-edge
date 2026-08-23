package migrations

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// testMigrations 注入两条简单 migration，用于在不依赖真实 embed SQL 的情况下
// 验证 RunMigrations 的版本记录、checksum、幂等与 fail-closed 行为。
func testMigrations() []Migration {
	return []Migration{
		{
			ID:       "mysql/0001-test",
			Checksum: sha256Hex("stmt-a;stmt-b"),
			Statements: []string{
				"CREATE TABLE IF NOT EXISTS t1 (id INT)",
				"CREATE TABLE IF NOT EXISTS t2 (id INT)",
			},
		},
	}
}

// newMock 建立 sqlmock 连接并注入 ReplaceDB。
func newMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, mock
}

func expectEnsureMetaTable(mock sqlmock.Sqlmock) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS aiops_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectLock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs("aiops_migrate", int64(30)).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs("aiops_migrate").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(int64(1)))
}

func expectAppliedRows(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT migration_id, checksum FROM aiops_schema_migrations").WillReturnRows(rows)
}

func TestRunMigrationsAppliesOnceAndIsIdempotent(t *testing.T) {
	db, mock := newMock(t)
	expectEnsureMetaTable(mock)
	expectLock(mock)
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}))

	// 首次：执行 2 条语句 + 插入 1 条 migration 记录
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS t1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS t2").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO aiops_schema_migrations").
		WithArgs("mysql/0001-test", sha256Hex("stmt-a;stmt-b")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := RunMigrations(db, testMigrations()); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// 二次：checksum 相同 → skip（不再 INSERT，不执行语句）
	expectEnsureMetaTable(mock)
	expectLock(mock)
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}).
		AddRow("mysql/0001-test", sha256Hex("stmt-a;stmt-b")))

	if err := RunMigrations(db, testMigrations()); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRunMigrationsFailsClosedOnChecksumMismatch(t *testing.T) {
	db, mock := newMock(t)
	expectEnsureMetaTable(mock)
	expectLock(mock)
	// DB 里已应用同 migration_id 但 checksum 不同（内容被篡改）
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}).
		AddRow("mysql/0001-test", sha256Hex("tampered")))

	err := RunMigrations(db, testMigrations())
	if err == nil {
		t.Fatal("RunMigrations with checksum mismatch error = nil, want fail closed")
	}
	if !errors.Is(err, ErrSchemaChecksumMismatch) {
		t.Fatalf("RunMigrations checksum mismatch error = %v, want ErrSchemaChecksumMismatch", err)
	}
}

func TestRequireCurrentVersionOKWhenAllPresentAndChecksumsMatch(t *testing.T) {
	db, mock := newMock(t)
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}).
		AddRow("mysql/0001-test", sha256Hex("stmt-a;stmt-b")))

	if err := RequireCurrentVersion(db, testMigrations()); err != nil {
		t.Fatalf("RequireCurrentVersion (all ok) error = %v", err)
	}
}

func TestRequireCurrentVersionFailsClosedOnMissingMigration(t *testing.T) {
	db, mock := newMock(t)
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}))

	err := RequireCurrentVersion(db, testMigrations())
	if err == nil {
		t.Fatal("RequireCurrentVersion (missing) error = nil, want ErrSchemaOutdated")
	}
	if !errors.Is(err, ErrSchemaOutdated) {
		t.Fatalf("RequireCurrentVersion missing error = %v, want ErrSchemaOutdated", err)
	}
}

func TestRequireCurrentVersionFailsClosedOnChecksumMismatch(t *testing.T) {
	db, mock := newMock(t)
	expectAppliedRows(mock, sqlmock.NewRows([]string{"migration_id", "checksum"}).
		AddRow("mysql/0001-test", sha256Hex("tampered")))

	err := RequireCurrentVersion(db, testMigrations())
	if err == nil {
		t.Fatal("RequireCurrentVersion (checksum mismatch) error = nil, want ErrSchemaChecksumMismatch")
	}
	if !errors.Is(err, ErrSchemaChecksumMismatch) {
		t.Fatalf("RequireCurrentVersion mismatch error = %v, want ErrSchemaChecksumMismatch", err)
	}
}
