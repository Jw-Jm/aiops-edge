package store

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type nonNilTimeArgument struct{}

func (nonNilTimeArgument) Match(value driver.Value) bool {
	_, ok := value.(time.Time)
	return ok
}

const (
	chatAuditAuthSession = "55555555-5555-4555-8555-555555555555"
	chatAuditSession     = "66666666-6666-4666-8666-666666666666"
	chatAuditTurn        = "77777777-7777-4777-8777-777777777777"
	chatAuditCall        = "88888888-8888-4888-8888-888888888888"
	chatAuditRun         = "99999999-9999-4999-8999-999999999999"
	chatAuditUser        = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	chatAuditTenant      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	chatAuditCluster     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func chatAuditRecord() AIChatToolRun {
	now := time.Unix(1_700_000_000, 0).UTC()
	return AIChatToolRun{
		ChatToolRunID: chatAuditRun, PrincipalID: chatAuditUser,
		SessionID: chatAuditAuthSession, ChatSessionID: chatAuditSession,
		TurnID: chatAuditTurn, ToolCallID: chatAuditCall,
		TenantID: chatAuditTenant, ClusterID: chatAuditCluster,
		ToolName: "query_metrics.v1", Operation: "metrics",
		Capability: "observability.metrics.read", ArgsHash: strings.Repeat("a", 64),
		Status: "running", StartedAt: &now,
	}
}

func TestChatToolRunStartCreatesAndDoesNotPersistResultPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	record := chatAuditRecord()
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(record.ChatSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(record.PrincipalID, record.TenantID, record.ClusterID))
	mock.ExpectExec(`INSERT INTO ai_chat_tool_runs`).
		WithArgs(record.ChatToolRunID, record.PrincipalID, record.SessionID, record.ChatSessionID,
			record.TurnID, record.ToolCallID, record.TenantID, record.ClusterID, record.ToolName,
			record.Operation, record.Capability, record.ArgsHash, record.Status, record.StartedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	created, existing, err := (&AIChatToolRunDAO{}).Start(record)
	if err != nil || !created || existing != nil {
		t.Fatalf("Start() = created=%v existing=%v err=%v, want a new audit row", created, existing, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatToolRunStartDefaultsRequiredStartedAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	record := chatAuditRecord()
	record.StartedAt = nil
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(record.ChatSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(record.PrincipalID, record.TenantID, record.ClusterID))
	mock.ExpectExec(`INSERT INTO ai_chat_tool_runs`).
		WithArgs(record.ChatToolRunID, record.PrincipalID, record.SessionID, record.ChatSessionID,
			record.TurnID, record.ToolCallID, record.TenantID, record.ClusterID, record.ToolName,
			record.Operation, record.Capability, record.ArgsHash, record.Status, nonNilTimeArgument{}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	created, existing, err := (&AIChatToolRunDAO{}).Start(record)
	if err != nil || !created || existing != nil {
		t.Fatalf("Start() = created=%v existing=%v err=%v, want a durable audit row", created, existing, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatToolRunStartReplaysSameKeyAndRejectsDifferentArgs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })
	mock.MatchExpectationsInOrder(false)

	record := chatAuditRecord()
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(record.ChatSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(record.PrincipalID, record.TenantID, record.ClusterID))
	mock.ExpectExec(`INSERT INTO ai_chat_tool_runs`).WillReturnError(errors.New("duplicate entry"))
	mock.ExpectQuery(`(?s)SELECT chat_tool_run_id.*FROM ai_chat_tool_runs WHERE chat_session_id=\? AND turn_id=\? AND tool_call_id=\?`).
		WithArgs(record.ChatSessionID, record.TurnID, record.ToolCallID).
		WillReturnRows(sqlmock.NewRows([]string{"chat_tool_run_id", "principal_id", "session_id", "chat_session_id", "turn_id", "tool_call_id", "tenant_id", "cluster_id", "tool_name", "operation", "capability", "args_hash", "status", "result_digest_sha256", "result_count", "error_code", "started_at", "completed_at", "created_at"}).
			AddRow(record.ChatToolRunID, record.PrincipalID, record.SessionID, record.ChatSessionID, record.TurnID, record.ToolCallID, record.TenantID, record.ClusterID, record.ToolName, record.Operation, record.Capability, record.ArgsHash, "success", nil, 3, nil, record.StartedAt, record.StartedAt, record.StartedAt))

	created, existing, err := (&AIChatToolRunDAO{}).Start(record)
	if err != nil || created || existing == nil || existing.ArgsHash != record.ArgsHash {
		t.Fatalf("same-key Start() = created=%v existing=%#v err=%v, want replay", created, existing, err)
	}

	different := record
	different.ArgsHash = strings.Repeat("b", 64)
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(different.ChatSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(different.PrincipalID, different.TenantID, different.ClusterID))
	mock.ExpectExec(`INSERT INTO ai_chat_tool_runs`).WillReturnError(errors.New("duplicate entry"))
	mock.ExpectQuery(`(?s)SELECT chat_tool_run_id.*FROM ai_chat_tool_runs WHERE chat_session_id=\? AND turn_id=\? AND tool_call_id=\?`).
		WithArgs(different.ChatSessionID, different.TurnID, different.ToolCallID).
		WillReturnRows(sqlmock.NewRows([]string{"chat_tool_run_id", "principal_id", "session_id", "chat_session_id", "turn_id", "tool_call_id", "tenant_id", "cluster_id", "tool_name", "operation", "capability", "args_hash", "status", "result_digest_sha256", "result_count", "error_code", "started_at", "completed_at", "created_at"}).
			AddRow(record.ChatToolRunID, record.PrincipalID, record.SessionID, record.ChatSessionID, record.TurnID, record.ToolCallID, record.TenantID, record.ClusterID, record.ToolName, record.Operation, record.Capability, record.ArgsHash, "success", nil, 3, nil, record.StartedAt, record.StartedAt, record.StartedAt))
	_, _, err = (&AIChatToolRunDAO{}).Start(different)
	if !errors.Is(err, ErrChatToolIdempotencyConflict) {
		t.Fatalf("different-args Start() error = %v, want ErrChatToolIdempotencyConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatToolRunFinishChecksOwnerAndWritesTerminalDigest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	record := chatAuditRecord()
	mock.ExpectExec(`UPDATE ai_chat_tool_runs SET status = \?, result_digest_sha256 = \?, result_count = \?, error_code = \?, completed_at = \? WHERE chat_tool_run_id = \? AND principal_id = \? AND session_id = \? AND tenant_id = \? AND cluster_id = \? AND status = 'running'`).
		WithArgs("success", strings.Repeat("c", 64), int64(3), nil, sqlmock.AnyArg(), record.ChatToolRunID, record.PrincipalID, record.SessionID, record.TenantID, record.ClusterID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := (&AIChatToolRunDAO{}).Finish(record.ChatToolRunID, record.PrincipalID, record.SessionID, record.TenantID, record.ClusterID, "success", strings.Repeat("c", 64), 3, ""); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
