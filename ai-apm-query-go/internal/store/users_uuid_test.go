package store

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUserDAOCreateStoresCanonicalUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectExec("INSERT INTO users \\(user_uuid, username, password_hash, display_name, role, email, status, is_approver\\)").
		WithArgs("alice", "hash", "Alice", "user", "alice@example.com", 1, 0).
		WillReturnResult(sqlmock.NewResult(7, 1))
	if _, err := (&UserDAO{}).Create(&User{Username: "alice", PasswordHash: "hash", DisplayName: "Alice", Role: "user", Email: "alice@example.com", Status: 1}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserDAOSeedAdminStoresCanonicalUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectExec("INSERT IGNORE INTO users \\(user_uuid, username, password_hash, display_name, role, must_change_password\\)").
		WithArgs("hash").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := (&UserDAO{}).SeedAdmin("hash"); err != nil {
		t.Fatalf("SeedAdmin() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserDAOChangePasswordUpdatesHashAndRevokesSessionsAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE users SET password_hash=\\?, must_change_password=0 WHERE user_uuid=\\? AND status=1").
		WithArgs("new-hash", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE auth_sessions SET status='revoked', revoked_at=UTC_TIMESTAMP\\(\\), token_version=token_version\\+1 WHERE user_uuid=\\? AND status='active'").
		WithArgs("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := (&UserDAO{}).ChangePassword("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "new-hash"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
