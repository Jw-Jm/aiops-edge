package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

type AIRunGraphContext struct {
	RunID               string
	ContextVersion      int64
	TenantID            string
	ScopeKind           string
	PrimaryClusterID    string
	GraphSchemaVersion  int64
	GraphGeneration     int64
	EvidenceCutoffAt    interface{}
	TriggerEntityUID    string
	RootCauseEntityUID  string
	IsFinal             bool
	ContextJSON         string
	ContextDigestSHA256 string
}

type AIRunGraphContextDAO struct{}

func (d *AIRunGraphContextDAO) GetLatest(runID, tenantID string) (*AIRunGraphContext, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var context AIRunGraphContext
	var primary, root sql.NullString
	var final int
	err := conn.QueryRow(`SELECT run_id, context_version, tenant_id, scope_kind, primary_cluster_id, graph_schema_version,
    graph_generation, evidence_cutoff_at, trigger_entity_uid, root_cause_entity_uid, is_final, context_json, context_digest_sha256
    FROM ai_run_graph_contexts WHERE run_id=? AND tenant_id=? ORDER BY is_final DESC, context_version DESC LIMIT 1`, runID, tenantID).Scan(
		&context.RunID, &context.ContextVersion, &context.TenantID, &context.ScopeKind, &primary, &context.GraphSchemaVersion,
		&context.GraphGeneration, &context.EvidenceCutoffAt, &context.TriggerEntityUID, &root, &final, &context.ContextJSON,
		&context.ContextDigestSHA256)
	if err != nil {
		return nil, err
	}
	context.PrimaryClusterID, context.RootCauseEntityUID, context.IsFinal = primary.String, root.String, final == 1
	return &context, nil
}

func ContextDigestJSON(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (d *AIRunGraphContextDAO) Insert(context AIRunGraphContext) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if context.ContextJSON == "" {
		context.ContextJSON = "{}"
	}
	if context.ContextDigestSHA256 == "" {
		context.ContextDigestSHA256 = ContextDigestJSON(context.ContextJSON)
	}
	if len(context.ContextJSON) > 1<<20 {
		return fmt.Errorf("graph context exceeds 1 MiB")
	}
	_, err := conn.Exec(`INSERT INTO ai_run_graph_contexts
    (run_id, context_version, tenant_id, scope_kind, primary_cluster_id, graph_schema_version, graph_generation,
     evidence_cutoff_at, trigger_entity_uid, root_cause_entity_uid, is_final, context_json, context_digest_sha256)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE context_json=VALUES(context_json), context_digest_sha256=VALUES(context_digest_sha256),
      root_cause_entity_uid=VALUES(root_cause_entity_uid), is_final=VALUES(is_final)`, context.RunID, context.ContextVersion,
		context.TenantID, context.ScopeKind, nullableStr(context.PrimaryClusterID), context.GraphSchemaVersion, context.GraphGeneration,
		context.EvidenceCutoffAt, context.TriggerEntityUID, nullableStr(context.RootCauseEntityUID), boolInt(context.IsFinal),
		context.ContextJSON, context.ContextDigestSHA256)
	return err
}

func (d *AIRunGraphContextDAO) Get(runID string, version int64, tenantID string) (*AIRunGraphContext, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var context AIRunGraphContext
	var primary, root sql.NullString
	var final int
	err := conn.QueryRow(`SELECT run_id, context_version, tenant_id, scope_kind, primary_cluster_id, graph_schema_version,
    graph_generation, evidence_cutoff_at, trigger_entity_uid, root_cause_entity_uid, is_final, context_json, context_digest_sha256
    FROM ai_run_graph_contexts WHERE run_id=? AND context_version=? AND tenant_id=?`, runID, version, tenantID).Scan(
		&context.RunID, &context.ContextVersion, &context.TenantID, &context.ScopeKind, &primary, &context.GraphSchemaVersion,
		&context.GraphGeneration, &context.EvidenceCutoffAt, &context.TriggerEntityUID, &root, &final, &context.ContextJSON,
		&context.ContextDigestSHA256)
	if err != nil {
		return nil, err
	}
	context.PrimaryClusterID, context.RootCauseEntityUID, context.IsFinal = primary.String, root.String, final == 1
	return &context, nil
}

func (d *AIRunGraphContextDAO) ValidateJSON(raw string) error {
	var value interface{}
	if raw == "" || len(raw) > 1<<20 {
		return errors.New("invalid graph context size")
	}
	return json.Unmarshal([]byte(raw), &value)
}
