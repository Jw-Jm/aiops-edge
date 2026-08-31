package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

// ChatSession is the browser-facing, MySQL-owned chat transcript projection.
// The orchestrator may keep an internal checkpoint for execution, but it is
// never the authority for listing, loading, or deleting user sessions.
type ChatSession struct {
	SessionID string    `json:"session_id"`
	UserUUID  string    `json:"user_uuid,omitempty"`
	TenantID  string    `json:"tenant_id,omitempty"`
	ClusterID string    `json:"cluster_id,omitempty"`
	Intent    string    `json:"intent"`
	Service   string    `json:"service"`
	Preview   string    `json:"preview,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessage struct {
	ID        int64
	Role      string
	Kind      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

type AIChatSessionDAO struct{}

func chatDB() (*sql.DB, error) {
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	return db, nil
}

func (d *AIChatSessionDAO) EnsureSession(sessionID, userUUID, tenantID, clusterID, intent, service string) error {
	db, err := chatDB()
	if err != nil {
		return err
	}
	if sessionID == "" || userUUID == "" || tenantID == "" || clusterID == "" {
		return errors.New("invalid chat session scope")
	}
	// The old SELECT→INSERT sequence was racy: two Query API replicas could
	// both observe no row and one would lose the primary-key race, surfacing a
	// duplicate-key error to an otherwise valid first chat turn.  Insert a
	// missing row atomically and make the duplicate branch a no-op.  Crucially,
	// this never updates ownership fields; the owner check below remains the
	// authoritative scope guard for resumed sessions and cross-tenant probes.
	if _, err = db.Exec(`INSERT INTO ai_chat_sessions
		 (session_id,user_uuid,tenant_id,cluster_id,intent,service)
		 VALUES (?,?,?,?,?,?)
		 ON DUPLICATE KEY UPDATE session_id = session_id`,
		sessionID, userUUID, tenantID, clusterID, intent, service); err != nil {
		return err
	}

	var owner, ownerTenant, ownerCluster string
	if err = db.QueryRow(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=?`, sessionID).
		Scan(&owner, &ownerTenant, &ownerCluster); err != nil {
		return err
	}
	if owner != userUUID || ownerTenant != tenantID || ownerCluster != clusterID {
		return errors.New("chat session scope mismatch")
	}
	_, err = db.Exec(`UPDATE ai_chat_sessions SET intent=?,service=?,updated_at=CURRENT_TIMESTAMP(3) WHERE session_id=?`, intent, service, sessionID)
	return err
}

func (d *AIChatSessionDAO) AppendMessage(sessionID, userUUID, tenantID, clusterID, role, kind, content string, metadata map[string]any) error {
	return d.AppendMessageForTurn(sessionID, userUUID, tenantID, clusterID, "", role, kind, content, metadata)
}

// AppendMessageForTurn appends a durable card and makes retries for the same
// turn idempotent.  A blank turn_id keeps the legacy/test insertion shape for
// historical callers; canonical ProxyChat traffic always supplies a UUID.
func (d *AIChatSessionDAO) AppendMessageForTurn(sessionID, userUUID, tenantID, clusterID, turnID, role, kind, content string, metadata map[string]any) error {
	db, err := chatDB()
	if err != nil {
		return err
	}
	var owner string
	if err := db.QueryRow(`SELECT user_uuid FROM ai_chat_sessions
 WHERE session_id=? AND user_uuid=? AND tenant_id=? AND cluster_id=?`, sessionID, userUUID, tenantID, clusterID).Scan(&owner); err != nil {
		return err
	}
	var raw []byte
	if len(metadata) > 0 {
		raw, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	turnID = strings.TrimSpace(turnID)
	if turnID != "" {
		// The unique key (session_id, turn_id, role, kind) makes concurrent
		// retries converge on one row.  The no-op duplicate branch deliberately
		// does not overwrite content; the read-back below detects a reused turn
		// with a different payload and fails closed.
		if _, err = db.Exec(`INSERT INTO ai_chat_messages(session_id,turn_id,role,kind,content,metadata_json)
		 VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE id = id`,
			sessionID, turnID, role, kind, content, chatNullableJSON(raw)); err != nil {
			return err
		}
		var existingRole, existingKind, existingContent string
		var existingRaw sql.NullString
		if err = db.QueryRow(`SELECT role,kind,content,metadata_json FROM ai_chat_messages
 WHERE session_id=? AND turn_id=? AND role=? AND kind=?`, sessionID, turnID, role, kind).
			Scan(&existingRole, &existingKind, &existingContent, &existingRaw); err != nil {
			return err
		}
		if existingRole != role || existingKind != kind || existingContent != content || !chatMetadataEqual(existingRaw, raw) {
			return errors.New("chat message idempotency mismatch")
		}
		return nil
	}
	_, err = db.Exec(`INSERT INTO ai_chat_messages(session_id,role,kind,content,metadata_json)
		 VALUES (?,?,?,?,?)`, sessionID, role, kind, content, chatNullableJSON(raw))
	return err
}

func chatMetadataEqual(existing sql.NullString, want []byte) bool {
	if len(want) == 0 {
		return !existing.Valid || strings.TrimSpace(existing.String) == ""
	}
	if !existing.Valid || strings.TrimSpace(existing.String) == "" {
		return false
	}
	var gotValue, wantValue any
	if json.Unmarshal([]byte(existing.String), &gotValue) != nil || json.Unmarshal(want, &wantValue) != nil {
		return existing.String == string(want)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func chatNullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func (d *AIChatSessionDAO) List(userUUID, tenantID, clusterID string, limit int) ([]ChatSession, error) {
	db, err := chatDB()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(`SELECT s.session_id,s.intent,s.service,s.created_at,s.updated_at,
 COALESCE((SELECT m.content FROM ai_chat_messages m WHERE m.session_id=s.session_id
   AND m.role='user' ORDER BY m.id LIMIT 1),'')
 FROM ai_chat_sessions s WHERE s.user_uuid=? AND s.tenant_id=? AND s.cluster_id=?
 ORDER BY s.updated_at DESC LIMIT ?`, userUUID, tenantID, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChatSession, 0)
	for rows.Next() {
		var item ChatSession
		if err := rows.Scan(&item.SessionID, &item.Intent, &item.Service, &item.CreatedAt, &item.UpdatedAt, &item.Preview); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *AIChatSessionDAO) Get(sessionID, userUUID, tenantID, clusterID string) (*ChatSession, []ChatMessage, error) {
	db, err := chatDB()
	if err != nil {
		return nil, nil, err
	}
	var item ChatSession
	if err := db.QueryRow(`SELECT session_id,intent,service,created_at,updated_at
 FROM ai_chat_sessions WHERE session_id=? AND user_uuid=? AND tenant_id=? AND cluster_id=?`, sessionID, userUUID, tenantID, clusterID).
		Scan(&item.SessionID, &item.Intent, &item.Service, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, nil, err
	}
	rows, err := db.Query(`SELECT id,role,kind,content,metadata_json,created_at
 FROM ai_chat_messages WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	messages := make([]ChatMessage, 0)
	for rows.Next() {
		var m ChatMessage
		var raw sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Kind, &m.Content, &raw, &m.CreatedAt); err != nil {
			return nil, nil, err
		}
		if raw.Valid && raw.String != "" {
			_ = json.Unmarshal([]byte(raw.String), &m.Metadata)
		}
		messages = append(messages, m)
	}
	return &item, messages, rows.Err()
}

// GetTurn returns durable cards for one canonical turn after checking the
// session scope.  It is intentionally not exposed as a browser endpoint; the
// Query proxy uses it to replay a completed turn after a transport retry
// without invoking the LLM a second time.
func (d *AIChatSessionDAO) GetTurn(sessionID, userUUID, tenantID, clusterID, turnID string) ([]ChatMessage, error) {
	db, err := chatDB()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(turnID) == "" {
		return nil, errors.New("invalid chat turn id")
	}
	var owner string
	if err := db.QueryRow(`SELECT user_uuid FROM ai_chat_sessions
 WHERE session_id=? AND user_uuid=? AND tenant_id=? AND cluster_id=?`, sessionID, userUUID, tenantID, clusterID).Scan(&owner); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,role,kind,content,metadata_json,created_at
 FROM ai_chat_messages WHERE session_id=? AND turn_id=? ORDER BY id`, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]ChatMessage, 0)
	for rows.Next() {
		var m ChatMessage
		var raw sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Kind, &m.Content, &raw, &m.CreatedAt); err != nil {
			return nil, err
		}
		if raw.Valid && raw.String != "" {
			_ = json.Unmarshal([]byte(raw.String), &m.Metadata)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (d *AIChatSessionDAO) Delete(sessionID, userUUID, tenantID, clusterID string) (bool, error) {
	db, err := chatDB()
	if err != nil {
		return false, err
	}
	result, err := db.Exec(`DELETE FROM ai_chat_sessions WHERE session_id=? AND user_uuid=? AND tenant_id=? AND cluster_id=?`, sessionID, userUUID, tenantID, clusterID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (d *AIChatSessionDAO) Clear(userUUID, tenantID, clusterID string) (int64, error) {
	db, err := chatDB()
	if err != nil {
		return 0, err
	}
	result, err := db.Exec(`DELETE FROM ai_chat_sessions WHERE user_uuid=? AND tenant_id=? AND cluster_id=?`, userUUID, tenantID, clusterID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
