package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var canonicalChatUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// AIChatToolRun is the durable audit record for one read-only Chat tool call.
// It intentionally has no run_id/evidence relationship: Chat is not an
// Investigation Run and must not create an ai_tool_runs row as a substitute.
type AIChatToolRun struct {
	ChatToolRunID      string
	PrincipalID        string
	SessionID          string // authenticated login session
	ChatSessionID      string // durable ai_chat_sessions.session_id
	TurnID             string
	ToolCallID         string
	TenantID           string
	ClusterID          string
	ToolName           string
	Operation          string
	Capability         string
	ArgsHash           string
	Status             string
	ResultDigestSHA256 string
	ResultCount        int64
	ErrorCode          string
	StartedAt          *time.Time
	CompletedAt        *time.Time
	CreatedAt          time.Time
}

var (
	ErrChatToolIdempotencyConflict = errors.New("chat tool idempotency key reused with different args")
	ErrChatToolAuditNotFound       = errors.New("chat tool audit record not found")
	ErrChatToolAuditOwnership      = errors.New("chat tool audit ownership mismatch")
)

// AIChatToolRunDAO owns the ai_chat_tool_runs table.  It only persists audit
// metadata, never the raw query result or query text.
type AIChatToolRunDAO struct{}

func (d *AIChatToolRunDAO) Start(t AIChatToolRun) (bool, *AIChatToolRun, error) {
	if err := validateChatToolRun(t); err != nil {
		return false, nil, err
	}
	conn := GetDB()
	if conn == nil {
		return false, nil, errors.New("mysql unavailable")
	}
	var ownerUser, ownerTenant, ownerCluster string
	if err := conn.QueryRow(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=?`, t.ChatSessionID).
		Scan(&ownerUser, &ownerTenant, &ownerCluster); err != nil {
		return false, nil, err
	}
	if ownerUser != t.PrincipalID || ownerTenant != t.TenantID || ownerCluster != t.ClusterID {
		return false, nil, ErrChatToolAuditOwnership
	}
	status := t.Status
	if status == "" {
		status = "running"
	}
	started := nullableTime(t.StartedAt)
	_, err := conn.Exec(`INSERT INTO ai_chat_tool_runs
 (chat_tool_run_id,principal_id,session_id,chat_session_id,turn_id,tool_call_id,
  tenant_id,cluster_id,tool_name,operation,capability,args_hash,status,started_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ChatToolRunID, t.PrincipalID, t.SessionID, t.ChatSessionID, t.TurnID, t.ToolCallID,
		t.TenantID, t.ClusterID, t.ToolName, t.Operation, t.Capability, t.ArgsHash, status, started)
	if err == nil {
		return true, nil, nil
	}
	if !isDuplicateKey(err) {
		return false, nil, err
	}
	// A duplicate key is only a replay after the complete immutable identity is
	// read back and compared.  This prevents a colliding chat/session id from
	// crossing tenant, principal, cluster or argument boundaries.
	existing, lookupErr := d.getByKey(conn, t.ChatSessionID, t.TurnID, t.ToolCallID)
	if lookupErr != nil {
		return false, nil, lookupErr
	}
	if !sameChatToolIdentity(*existing, t) {
		return false, nil, ErrChatToolIdempotencyConflict
	}
	return false, existing, nil
}

func (d *AIChatToolRunDAO) Finish(chatToolRunID, principalID, sessionID, tenantID, clusterID,
	status, digest string, count int64, errorCode string) error {
	if chatToolRunID == "" || principalID == "" || sessionID == "" || tenantID == "" || clusterID == "" {
		return errors.New("chat tool finish identity required")
	}
	if !validChatToolStatus(status) || count < 0 {
		return errors.New("invalid chat tool terminal status")
	}
	if digest != "" && len(digest) != 64 {
		return errors.New("invalid chat tool result digest")
	}
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	now := time.Now()
	result, err := conn.Exec(`UPDATE ai_chat_tool_runs SET status = ?, result_digest_sha256 = ?,
 result_count = ?, error_code = ?, completed_at = ?
 WHERE chat_tool_run_id = ? AND principal_id = ? AND session_id = ? AND tenant_id = ?
   AND cluster_id = ? AND status = 'running'`,
		status, nullableStr(digest), count, nullableStr(errorCode), now,
		chatToolRunID, principalID, sessionID, tenantID, clusterID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return d.finishFailure(conn, chatToolRunID, principalID, sessionID, tenantID, clusterID)
	}
	return nil
}

func (d *AIChatToolRunDAO) finishFailure(conn *sql.DB, id, principalID, sessionID, tenantID, clusterID string) error {
	existing, err := d.getByID(conn, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChatToolAuditNotFound
		}
		return err
	}
	if existing.PrincipalID != principalID || existing.SessionID != sessionID ||
		existing.TenantID != tenantID || existing.ClusterID != clusterID {
		return ErrChatToolAuditOwnership
	}
	return fmt.Errorf("chat tool audit is not running: %s", existing.Status)
}

func (d *AIChatToolRunDAO) getByKey(conn *sql.DB, chatSessionID, turnID, toolCallID string) (*AIChatToolRun, error) {
	row := conn.QueryRow(`SELECT chat_tool_run_id,principal_id,session_id,chat_session_id,turn_id,
 tool_call_id,tenant_id,cluster_id,tool_name,operation,capability,args_hash,status,
 result_digest_sha256,result_count,error_code,started_at,completed_at,created_at
 FROM ai_chat_tool_runs WHERE chat_session_id=? AND turn_id=? AND tool_call_id=?`, chatSessionID, turnID, toolCallID)
	return scanChatToolRun(row)
}

func (d *AIChatToolRunDAO) getByID(conn *sql.DB, id string) (*AIChatToolRun, error) {
	row := conn.QueryRow(`SELECT chat_tool_run_id,principal_id,session_id,chat_session_id,turn_id,
 tool_call_id,tenant_id,cluster_id,tool_name,operation,capability,args_hash,status,
 result_digest_sha256,result_count,error_code,started_at,completed_at,created_at
 FROM ai_chat_tool_runs WHERE chat_tool_run_id=?`, id)
	return scanChatToolRun(row)
}

type chatToolRow interface{ Scan(...any) error }

func scanChatToolRun(row chatToolRow) (*AIChatToolRun, error) {
	var t AIChatToolRun
	var digest, errorCode sql.NullString
	var count sql.NullInt64
	var started, completed, created sql.NullTime
	err := row.Scan(&t.ChatToolRunID, &t.PrincipalID, &t.SessionID, &t.ChatSessionID,
		&t.TurnID, &t.ToolCallID, &t.TenantID, &t.ClusterID, &t.ToolName, &t.Operation,
		&t.Capability, &t.ArgsHash, &t.Status, &digest, &count, &errorCode,
		&started, &completed, &created)
	if err != nil {
		return nil, err
	}
	t.ResultDigestSHA256, t.ErrorCode, t.ResultCount = digest.String, errorCode.String, count.Int64
	if started.Valid {
		t.StartedAt = &started.Time
	}
	if completed.Valid {
		t.CompletedAt = &completed.Time
	}
	if created.Valid {
		t.CreatedAt = created.Time
	}
	return &t, nil
}

func validateChatToolRun(t AIChatToolRun) error {
	for name, value := range map[string]string{
		"chat_tool_run_id": t.ChatToolRunID, "principal_id": t.PrincipalID, "session_id": t.SessionID,
		"chat_session_id": t.ChatSessionID, "turn_id": t.TurnID, "tool_call_id": t.ToolCallID,
		"tenant_id": t.TenantID, "cluster_id": t.ClusterID,
	} {
		if !canonicalChatUUID.MatchString(value) {
			return fmt.Errorf("%s must be a canonical UUID", name)
		}
	}
	if strings.TrimSpace(t.ToolName) == "" || len(t.ToolName) > 128 ||
		strings.TrimSpace(t.Operation) == "" || len(t.Operation) > 64 ||
		strings.TrimSpace(t.Capability) == "" || len(t.Capability) > 128 {
		return errors.New("chat tool identity is incomplete")
	}
	if len(t.ArgsHash) != 64 || strings.Trim(t.ArgsHash, "0123456789abcdef") != "" {
		return errors.New("args_hash must be a lowercase sha256")
	}
	if !validChatToolStatus(t.Status) && t.Status != "" {
		return errors.New("invalid chat tool status")
	}
	return nil
}

func validChatToolStatus(status string) bool {
	switch status {
	case "running", "success", "no_data", "partial", "failed", "unavailable":
		return true
	default:
		return false
	}
}

func sameChatToolIdentity(a, b AIChatToolRun) bool {
	return a.PrincipalID == b.PrincipalID && a.SessionID == b.SessionID &&
		a.ChatSessionID == b.ChatSessionID && a.TurnID == b.TurnID && a.ToolCallID == b.ToolCallID &&
		a.TenantID == b.TenantID && a.ClusterID == b.ClusterID && a.ToolName == b.ToolName &&
		a.Operation == b.Operation && a.Capability == b.Capability && a.ArgsHash == b.ArgsHash
}
