package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type KnowledgeScopeType string

const (
	KnowledgePlatformCommon KnowledgeScopeType = "platform_common"
	KnowledgeCluster        KnowledgeScopeType = "cluster"
)

type KnowledgeStatus string

const (
	KnowledgeDraft         KnowledgeStatus = "draft"
	KnowledgePendingReview KnowledgeStatus = "pending_review"
	KnowledgePublished     KnowledgeStatus = "published"
	KnowledgeDisabled      KnowledgeStatus = "disabled"
)

type KnowledgeScope struct {
	TenantID  string
	ClusterID string
}

func (s KnowledgeScope) Allows(scopeType KnowledgeScopeType, clusterID string) bool {
	return strings.TrimSpace(s.TenantID) != "" && (scopeType == KnowledgePlatformCommon || (scopeType == KnowledgeCluster && strings.TrimSpace(s.ClusterID) != "" && s.ClusterID == clusterID))
}

type OperationsKnowledge struct {
	KnowledgeID      string             `json:"knowledge_id"`
	TenantID         string             `json:"tenant_id"`
	ScopeType        KnowledgeScopeType `json:"scope_type"`
	ClusterID        string             `json:"cluster_id,omitempty"`
	KnowledgeType    string             `json:"knowledge_type"`
	Title            string             `json:"title"`
	Summary          string             `json:"summary"`
	SourceKind       string             `json:"source_kind"`
	SourceRevision   string             `json:"source_revision,omitempty"`
	Status           KnowledgeStatus    `json:"status"`
	CurrentVersionID string             `json:"current_version_id,omitempty"`
	DraftVersionID   string             `json:"draft_version_id,omitempty"`
	CurrentVersion   int                `json:"current_version"`
	CreatedBy        string             `json:"created_by"`
	UpdatedBy        string             `json:"updated_by"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	DisabledAt       *time.Time         `json:"disabled_at,omitempty"`
}

type OperationsKnowledgeVersion struct {
	VersionID      string    `json:"version_id"`
	KnowledgeID    string    `json:"knowledge_id"`
	VersionNo      int       `json:"version_no"`
	Content        string    `json:"content"`
	ContentSHA256  string    `json:"content_checksum"`
	SourceKind     string    `json:"source_kind"`
	SourceRevision string    `json:"source_revision,omitempty"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type KnowledgeInput struct {
	ScopeType      KnowledgeScopeType `json:"scope_type"`
	ClusterID      string             `json:"cluster_id,omitempty"`
	KnowledgeType  string             `json:"knowledge_type"`
	Title          string             `json:"title"`
	Summary        string             `json:"summary"`
	Content        string             `json:"content"`
	SourceKind     string             `json:"source_kind"`
	SourceRevision string             `json:"source_revision,omitempty"`
}

type KnowledgeReview struct {
	ReviewID    int64     `json:"review_id"`
	KnowledgeID string    `json:"knowledge_id"`
	VersionID   string    `json:"version_id"`
	ReviewerID  string    `json:"reviewer_id"`
	Decision    string    `json:"decision"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
}

type KnowledgeIndexState struct {
	KnowledgeID string     `json:"knowledge_id"`
	VersionID   string     `json:"version_id"`
	Status      string     `json:"status"`
	Attempt     int        `json:"attempt"`
	LastError   string     `json:"last_error,omitempty"`
	IndexedAt   *time.Time `json:"indexed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type KnowledgeIndexOutbox struct {
	OutboxID    string     `json:"outbox_id"`
	KnowledgeID string     `json:"knowledge_id"`
	VersionID   string     `json:"version_id"`
	EventKind   string     `json:"event_kind"`
	Status      string     `json:"status"`
	Attempt     int        `json:"attempt"`
	LastError   string     `json:"last_error,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	IndexedAt   *time.Time `json:"indexed_at,omitempty"`
}

type OperationsKnowledgeDAO struct{}

func validateKnowledgeInput(scope KnowledgeScope, input KnowledgeInput) error {
	if strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Content) == "" {
		return errors.New("knowledge tenant, title and content are required")
	}
	if input.ScopeType != KnowledgePlatformCommon && input.ScopeType != KnowledgeCluster {
		return errors.New("invalid knowledge scope_type")
	}
	if input.ScopeType == KnowledgePlatformCommon && strings.TrimSpace(input.ClusterID) != "" {
		return errors.New("platform_common knowledge cannot carry cluster_id")
	}
	if input.ScopeType == KnowledgeCluster && strings.TrimSpace(input.ClusterID) == "" {
		return errors.New("cluster knowledge requires cluster_id")
	}
	if strings.TrimSpace(input.KnowledgeType) == "" {
		return errors.New("knowledge_type is required")
	}
	return nil
}

func checksumKnowledge(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func deterministicKnowledgeOutboxID(knowledgeID, versionID string) string {
	value := checksumKnowledge(knowledgeID + "\x1f" + versionID + "\x1fpublish")
	return fmt.Sprintf("%s-%s-%s-%s-%s", value[0:8], value[8:12], "5"+value[13:16], "8"+value[17:20], value[20:32])
}

func knowledgeScopeClause(scope KnowledgeScope) (string, []interface{}) {
	return "tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?))", []interface{}{scope.TenantID, scope.ClusterID}
}

func knowledgeScopeClauseQualified(scope KnowledgeScope, alias string) (string, []interface{}) {
	return alias + ".tenant_id=? AND (" + alias + ".scope_type='platform_common' OR (" + alias + ".scope_type='cluster' AND " + alias + ".cluster_id=?))", []interface{}{scope.TenantID, scope.ClusterID}
}

func (d *OperationsKnowledgeDAO) Create(ctx context.Context, scope KnowledgeScope, actor string, input KnowledgeInput) (*OperationsKnowledge, *OperationsKnowledgeVersion, error) {
	if err := validateKnowledgeInput(scope, input); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(actor) == "" {
		return nil, nil, errors.New("knowledge actor is required")
	}
	db := GetDB()
	if db == nil {
		return nil, nil, ErrMySQLUnavailable
	}
	knowledgeID, versionID := NewUUIDv4(), NewUUIDv4()
	if input.SourceKind == "" {
		input.SourceKind = "manual"
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO operations_knowledge
 (knowledge_id,tenant_id,scope_type,cluster_id,knowledge_type,title,summary,source_kind,source_revision,status,draft_version_id,created_by,updated_by)
 VALUES (?,?,?,?,?,?,?,?, 'draft', ?,?,?,?)`, knowledgeID, scope.TenantID, input.ScopeType, nullableString(input.ClusterID), input.KnowledgeType, input.Title, input.Summary, input.SourceKind, nullableString(input.SourceRevision), versionID, actor, actor)
	if err != nil {
		return nil, nil, err
	}
	version := &OperationsKnowledgeVersion{VersionID: versionID, KnowledgeID: knowledgeID, VersionNo: 1, Content: input.Content, ContentSHA256: checksumKnowledge(input.Content), SourceKind: input.SourceKind, SourceRevision: input.SourceRevision, CreatedBy: actor}
	_, err = tx.ExecContext(ctx, `INSERT INTO operations_knowledge_versions
 (version_id,knowledge_id,version_no,content,content_checksum,source_kind,source_revision,created_by)
 VALUES (?,?,?,?,?,?,?,?)`, version.VersionID, version.KnowledgeID, version.VersionNo, version.Content, version.ContentSHA256, version.SourceKind, nullableString(version.SourceRevision), version.CreatedBy)
	if err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &OperationsKnowledge{KnowledgeID: knowledgeID, TenantID: scope.TenantID, ScopeType: input.ScopeType, ClusterID: input.ClusterID, KnowledgeType: input.KnowledgeType, Title: input.Title, Summary: input.Summary, SourceKind: input.SourceKind, SourceRevision: input.SourceRevision, Status: KnowledgeDraft, DraftVersionID: versionID, UpdatedBy: actor, CreatedBy: actor}, version, nil
}

func scanKnowledge(scanner interface{ Scan(...interface{}) error }) (*OperationsKnowledge, error) {
	var item OperationsKnowledge
	var scope, status, cluster, current, draft, sourceRevision sql.NullString
	var disabled sql.NullTime
	if err := scanner.Scan(&item.KnowledgeID, &item.TenantID, &scope, &cluster, &item.KnowledgeType, &item.Title, &item.Summary, &item.SourceKind, &sourceRevision, &status, &current, &draft, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &disabled); err != nil {
		return nil, err
	}
	item.ScopeType, item.Status = KnowledgeScopeType(scope.String), KnowledgeStatus(status.String)
	item.ClusterID, item.CurrentVersionID, item.DraftVersionID, item.SourceRevision = cluster.String, current.String, draft.String, sourceRevision.String
	if disabled.Valid {
		item.DisabledAt = &disabled.Time
	}
	return &item, nil
}

const knowledgeSelect = `SELECT knowledge_id,tenant_id,scope_type,cluster_id,knowledge_type,title,summary,source_kind,source_revision,status,current_version_id,draft_version_id,created_by,updated_by,created_at,updated_at,disabled_at FROM operations_knowledge`

func (d *OperationsKnowledgeDAO) Get(ctx context.Context, scope KnowledgeScope, knowledgeID string) (*OperationsKnowledge, error) {
	where, args := knowledgeScopeClause(scope)
	row := GetDB()
	if row == nil {
		return nil, ErrMySQLUnavailable
	}
	return scanKnowledge(row.QueryRowContext(ctx, knowledgeSelect+" WHERE "+where+" AND knowledge_id=?", append(args, knowledgeID)...))
}

func (d *OperationsKnowledgeDAO) List(ctx context.Context, scope KnowledgeScope, knowledgeType, status string, limit int) ([]OperationsKnowledge, error) {
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, args := knowledgeScopeClause(scope)
	query := knowledgeSelect + " WHERE " + where
	if strings.TrimSpace(knowledgeType) != "" {
		query += " AND knowledge_type=?"
		args = append(args, knowledgeType)
	}
	if strings.TrimSpace(status) != "" {
		query += " AND status=?"
		args = append(args, status)
	}
	query += " ORDER BY updated_at DESC, knowledge_id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OperationsKnowledge, 0)
	for rows.Next() {
		item, scanErr := scanKnowledge(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

func (d *OperationsKnowledgeDAO) Submit(ctx context.Context, scope KnowledgeScope, knowledgeID, actor string) error {
	where, args := knowledgeScopeClause(scope)
	args = append(args, actor, knowledgeID)
	db := GetDB()
	if db == nil {
		return ErrMySQLUnavailable
	}
	result, err := db.ExecContext(ctx, "UPDATE operations_knowledge SET status='pending_review', updated_by=?, updated_at=NOW(3) WHERE "+where+" AND knowledge_id=? AND status='draft'", args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("knowledge is not an editable draft")
	}
	return nil
}

func (d *OperationsKnowledgeDAO) Review(ctx context.Context, scope KnowledgeScope, knowledgeID, reviewer, decision, reason string) error {
	decision, reason = strings.ToLower(strings.TrimSpace(decision)), strings.TrimSpace(reason)
	if decision != "approve" && decision != "reject" {
		return errors.New("review decision must be approve or reject")
	}
	if decision == "reject" && reason == "" {
		return errors.New("rejection reason is required")
	}
	db := GetDB()
	if db == nil {
		return ErrMySQLUnavailable
	}
	where, args := knowledgeScopeClause(scope)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	item, err := scanKnowledge(tx.QueryRowContext(ctx, knowledgeSelect+" WHERE "+where+" AND knowledge_id=? FOR UPDATE", append(args, knowledgeID)...))
	if err != nil {
		return err
	}
	if item.Status == KnowledgePublished && decision == "approve" {
		return tx.Commit()
	}
	if item.Status != KnowledgePendingReview {
		return errors.New("knowledge is not pending review")
	}
	versionID := item.DraftVersionID
	if versionID == "" {
		versionID = item.CurrentVersionID
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO operations_knowledge_reviews (knowledge_id,version_id,reviewer_id,decision,reason) VALUES (?,?,?,?,?)`, knowledgeID, versionID, reviewer, decision, reason)
	if err != nil {
		return err
	}
	if decision == "reject" {
		_, err = tx.ExecContext(ctx, `UPDATE operations_knowledge SET status='draft', updated_by=?, updated_at=NOW(3) WHERE knowledge_id=?`, reviewer, knowledgeID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE operations_knowledge SET status='published', current_version_id=?, draft_version_id=NULL, updated_by=?, updated_at=NOW(3) WHERE knowledge_id=?`, versionID, reviewer, knowledgeID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO operations_knowledge_index_outbox (outbox_id,knowledge_id,version_id,event_kind,status) VALUES (?,?,?,'publish','pending')`, deterministicKnowledgeOutboxID(knowledgeID, versionID), knowledgeID, versionID)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *OperationsKnowledgeDAO) CreateRevision(ctx context.Context, scope KnowledgeScope, knowledgeID, actor string, input KnowledgeInput) (*OperationsKnowledgeVersion, error) {
	if strings.TrimSpace(input.Content) == "" {
		return nil, errors.New("knowledge content is required")
	}
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	where, args := knowledgeScopeClause(scope)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	item, err := scanKnowledge(tx.QueryRowContext(ctx, knowledgeSelect+" WHERE "+where+" AND knowledge_id=? FOR UPDATE", append(args, knowledgeID)...))
	if err != nil {
		return nil, err
	}
	if item.Status == KnowledgeDisabled {
		return nil, errors.New("disabled knowledge cannot be revised")
	}
	if item.SourceKind == "playbook" || item.SourceKind == "git_playbook" {
		return nil, errors.New("git playbook revisions are managed by source control")
	}
	var versionNo int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM operations_knowledge_versions WHERE knowledge_id=?`, knowledgeID).Scan(&versionNo); err != nil {
		return nil, err
	}
	version := &OperationsKnowledgeVersion{VersionID: NewUUIDv4(), KnowledgeID: knowledgeID, VersionNo: versionNo, Content: input.Content, ContentSHA256: checksumKnowledge(input.Content), SourceKind: firstNonEmptyStore(input.SourceKind, item.SourceKind), SourceRevision: firstNonEmptyStore(input.SourceRevision, item.SourceRevision), CreatedBy: actor}
	_, err = tx.ExecContext(ctx, `INSERT INTO operations_knowledge_versions (version_id,knowledge_id,version_no,content,content_checksum,source_kind,source_revision,created_by) VALUES (?,?,?,?,?,?,?,?)`, version.VersionID, version.KnowledgeID, version.VersionNo, version.Content, version.ContentSHA256, version.SourceKind, nullableString(version.SourceRevision), version.CreatedBy)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE operations_knowledge SET status='draft', draft_version_id=?, updated_by=?, updated_at=NOW(3) WHERE knowledge_id=?`, version.VersionID, actor, knowledgeID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return version, nil
}

func firstNonEmptyStore(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (d *OperationsKnowledgeDAO) Disable(ctx context.Context, scope KnowledgeScope, knowledgeID, actor string) error {
	where, args := knowledgeScopeClause(scope)
	args = append(args, actor, knowledgeID)
	db := GetDB()
	if db == nil {
		return ErrMySQLUnavailable
	}
	_, err := db.ExecContext(ctx, "UPDATE operations_knowledge SET status='disabled', disabled_at=NOW(3), updated_by=?, updated_at=NOW(3) WHERE "+where+" AND knowledge_id=? AND status<>'disabled'", args...)
	return err
}

func (d *OperationsKnowledgeDAO) GetVersion(ctx context.Context, scope KnowledgeScope, knowledgeID, versionID string) (*OperationsKnowledgeVersion, error) {
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	where, args := knowledgeScopeClauseQualified(scope, "k")
	args = append(args, versionID, knowledgeID)
	var version OperationsKnowledgeVersion
	var revision sql.NullString
	err := db.QueryRowContext(ctx, `SELECT v.version_id,v.knowledge_id,v.version_no,v.content,v.content_checksum,v.source_kind,v.source_revision,v.created_by,v.created_at
 FROM operations_knowledge_versions v JOIN operations_knowledge k ON k.knowledge_id=v.knowledge_id
 WHERE `+where+" AND v.version_id=? AND v.knowledge_id=?", args...).Scan(&version.VersionID, &version.KnowledgeID, &version.VersionNo, &version.Content, &version.ContentSHA256, &version.SourceKind, &revision, &version.CreatedBy, &version.CreatedAt)
	version.SourceRevision = revision.String
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (d *OperationsKnowledgeDAO) IsCurrentPublished(ctx context.Context, scope KnowledgeScope, knowledgeID, versionID string) (bool, error) {
	db := GetDB()
	if db == nil {
		return false, ErrMySQLUnavailable
	}
	where, args := knowledgeScopeClause(scope)
	args = append(args, knowledgeID, versionID)
	var found int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM operations_knowledge WHERE `+where+` AND knowledge_id=? AND status='published' AND current_version_id=? LIMIT 1`, args...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && found == 1, err
}

func (d *OperationsKnowledgeDAO) ListIndexStates(ctx context.Context, scope KnowledgeScope, limit int) ([]KnowledgeIndexState, error) {
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, args := knowledgeScopeClauseQualified(scope, "k")
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, `SELECT s.knowledge_id,s.version_id,s.status,s.attempt,s.last_error,s.indexed_at,s.updated_at FROM operations_knowledge_index_state s JOIN operations_knowledge k ON k.knowledge_id=s.knowledge_id WHERE `+where+" ORDER BY s.updated_at DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []KnowledgeIndexState{}
	for rows.Next() {
		var item KnowledgeIndexState
		var indexed sql.NullTime
		if err := rows.Scan(&item.KnowledgeID, &item.VersionID, &item.Status, &item.Attempt, &item.LastError, &indexed, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if indexed.Valid {
			item.IndexedAt = &indexed.Time
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *OperationsKnowledgeDAO) ScanIndexOutbox(ctx context.Context, limit int) ([]KnowledgeIndexOutbox, error) {
	db := GetDB()
	if db == nil {
		return nil, ErrMySQLUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT outbox_id,knowledge_id,version_id,event_kind,status,attempt,last_error,next_retry_at,indexed_at FROM operations_knowledge_index_outbox WHERE status='pending' AND (next_retry_at IS NULL OR next_retry_at<=NOW(3)) ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []KnowledgeIndexOutbox{}
	for rows.Next() {
		var item KnowledgeIndexOutbox
		var nextRetry, indexed sql.NullTime
		var lastError sql.NullString
		if err := rows.Scan(&item.OutboxID, &item.KnowledgeID, &item.VersionID, &item.EventKind, &item.Status, &item.Attempt, &lastError, &nextRetry, &indexed); err != nil {
			return nil, err
		}
		item.LastError = lastError.String
		if nextRetry.Valid {
			item.NextRetryAt = &nextRetry.Time
		}
		if indexed.Valid {
			item.IndexedAt = &indexed.Time
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *OperationsKnowledgeDAO) EnqueuePublishedIndex(ctx context.Context, scope KnowledgeScope) (int, error) {
	db := GetDB()
	if db == nil {
		return 0, ErrMySQLUnavailable
	}
	where, args := knowledgeScopeClause(scope)
	rows, err := db.QueryContext(ctx, `SELECT knowledge_id,current_version_id FROM operations_knowledge WHERE `+where+` AND status='published' AND current_version_id IS NOT NULL`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type pair struct{ knowledgeID, versionID string }
	pairs := []pair{}
	for rows.Next() {
		var item pair
		if err := rows.Scan(&item.knowledgeID, &item.versionID); err != nil {
			return 0, err
		}
		pairs = append(pairs, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range pairs {
		if _, err := db.ExecContext(ctx, `INSERT IGNORE INTO operations_knowledge_index_outbox (outbox_id,knowledge_id,version_id,event_kind,status) VALUES (?,?,?,'publish','pending')`, deterministicKnowledgeOutboxID(item.knowledgeID, item.versionID), item.knowledgeID, item.versionID); err != nil {
			return 0, err
		}
	}
	return len(pairs), nil
}

func (d *OperationsKnowledgeDAO) MarkIndexSucceeded(ctx context.Context, outboxID, knowledgeID, versionID string) error {
	db := GetDB()
	if db == nil {
		return ErrMySQLUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE operations_knowledge_index_outbox SET status='indexed',indexed_at=NOW(3),last_error=NULL WHERE outbox_id=? AND knowledge_id=? AND version_id=?`, outboxID, knowledgeID, versionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operations_knowledge_index_state (knowledge_id,version_id,status,attempt,last_error,indexed_at) VALUES (?,?, 'indexed',0,NULL,NOW(3)) ON DUPLICATE KEY UPDATE status='indexed',last_error=NULL,indexed_at=NOW(3),updated_at=NOW(3)`, knowledgeID, versionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *OperationsKnowledgeDAO) MarkIndexFailed(ctx context.Context, outboxID, knowledgeID, versionID, sanitizedError string, retryAt time.Time) error {
	db := GetDB()
	if db == nil {
		return ErrMySQLUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE operations_knowledge_index_outbox SET status='pending',attempt=attempt+1,last_error=?,next_retry_at=? WHERE outbox_id=? AND knowledge_id=? AND version_id=?`, sanitizedError, retryAt, outboxID, knowledgeID, versionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operations_knowledge_index_state (knowledge_id,version_id,status,attempt,last_error) VALUES (?,?, 'failed',1,?) ON DUPLICATE KEY UPDATE status='failed',attempt=attempt+1,last_error=?,updated_at=NOW(3)`, knowledgeID, versionID, sanitizedError, sanitizedError); err != nil {
		return err
	}
	return tx.Commit()
}
