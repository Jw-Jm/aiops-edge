package store

import (
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	chatTestSession = "11111111-1111-4111-8111-111111111111"
	chatTestUser    = "22222222-2222-4222-8222-222222222222"
	chatTestTenant  = "33333333-3333-4333-8333-333333333333"
	chatTestCluster = "44444444-4444-4444-8444-444444444444"
)

func TestEnsureSessionUsesAtomicUpsertAndPreservesOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	mock.ExpectExec(`INSERT INTO ai_chat_sessions[\s\S]*ON DUPLICATE KEY UPDATE session_id = session_id`).
		WithArgs(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(chatTestSession).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).
			AddRow(chatTestUser, chatTestTenant, chatTestCluster))
	mock.ExpectExec(`UPDATE ai_chat_sessions SET intent=\?,service=\?,updated_at=CURRENT_TIMESTAMP\(3\) WHERE session_id=\?`).
		WithArgs("diagnosis", "orders", chatTestSession).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := (&AIChatSessionDAO{}).EnsureSession(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders"); err != nil {
		t.Fatalf("EnsureSession() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSessionRejectsExistingSessionFromAnotherScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	mock.ExpectExec(`INSERT INTO ai_chat_sessions[\s\S]*ON DUPLICATE KEY UPDATE session_id = session_id`).
		WithArgs(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(chatTestSession).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).
			AddRow("99999999-9999-4999-8999-999999999999", chatTestTenant, chatTestCluster))

	err = (&AIChatSessionDAO{}).EnsureSession(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders")
	if err == nil || !strings.Contains(err.Error(), "scope mismatch") {
		t.Fatalf("EnsureSession() error = %v, want scope mismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSessionConcurrentFirstTurnUsesIdempotentUpsert(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true), sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })
	mock.MatchExpectationsInOrder(false)

	for i := 0; i < 2; i++ {
		mock.ExpectExec(`INSERT INTO ai_chat_sessions[\s\S]*ON DUPLICATE KEY UPDATE session_id = session_id`).
			WithArgs(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
			WithArgs(chatTestSession).
			WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).
				AddRow(chatTestUser, chatTestTenant, chatTestCluster))
		mock.ExpectExec(`UPDATE ai_chat_sessions SET intent=\?,service=\?,updated_at=CURRENT_TIMESTAMP\(3\) WHERE session_id=\?`).
			WithArgs("diagnosis", "orders", chatTestSession).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := (&AIChatSessionDAO{}).EnsureSession(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster, "diagnosis", "orders"); err != nil {
				t.Errorf("concurrent EnsureSession() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendMessageForTurnConvergesOnRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
			WithArgs(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster).
			WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(chatTestUser))
		mock.ExpectExec(`INSERT INTO ai_chat_messages\(session_id,turn_id,role,kind,content,metadata_json\)[\s\S]*ON DUPLICATE KEY UPDATE id = id`).
			WithArgs(chatTestSession, "55555555-5555-4555-8555-555555555555", "user", "", "diagnosis", nil).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT role,kind,content,metadata_json FROM ai_chat_messages[\s\S]*WHERE session_id=\? AND turn_id=\? AND role=\? AND kind=\?`).
			WithArgs(chatTestSession, "55555555-5555-4555-8555-555555555555", "user", "").
			WillReturnRows(sqlmock.NewRows([]string{"role", "kind", "content", "metadata_json"}).AddRow("user", "", "diagnosis", nil))
	}

	for i := 0; i < 2; i++ {
		if err := (&AIChatSessionDAO{}).AppendMessageForTurn(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster,
			"55555555-5555-4555-8555-555555555555", "user", "", "diagnosis", nil); err != nil {
			t.Fatalf("retry %d AppendMessageForTurn() error = %v", i, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendMessageForTurnRejectsPayloadReuse(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	mock.ExpectQuery(`SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(chatTestUser))
	mock.ExpectExec(`INSERT INTO ai_chat_messages\(session_id,turn_id,role,kind,content,metadata_json\)[\s\S]*ON DUPLICATE KEY UPDATE id = id`).
		WithArgs(chatTestSession, "55555555-5555-4555-8555-555555555555", "user", "", "new payload", nil).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT role,kind,content,metadata_json FROM ai_chat_messages[\s\S]*WHERE session_id=\? AND turn_id=\? AND role=\? AND kind=\?`).
		WithArgs(chatTestSession, "55555555-5555-4555-8555-555555555555", "user", "").
		WillReturnRows(sqlmock.NewRows([]string{"role", "kind", "content", "metadata_json"}).AddRow("user", "", "original payload", nil))

	err = (&AIChatSessionDAO{}).AppendMessageForTurn(chatTestSession, chatTestUser, chatTestTenant, chatTestCluster,
		"55555555-5555-4555-8555-555555555555", "user", "", "new payload", nil)
	if err == nil || !strings.Contains(err.Error(), "idempotency mismatch") {
		t.Fatalf("AppendMessageForTurn() error = %v, want idempotency mismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
