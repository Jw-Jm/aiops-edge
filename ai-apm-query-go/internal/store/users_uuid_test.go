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

	mock.ExpectExec("INSERT IGNORE INTO users \\(user_uuid, username, password_hash, display_name, role\\)").
		WithArgs("hash").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := (&UserDAO{}).SeedAdmin("hash"); err != nil {
		t.Fatalf("SeedAdmin() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
