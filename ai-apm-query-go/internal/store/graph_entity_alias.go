package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

type GraphEntityAlias struct {
	AliasID            int64
	TenantID           string
	ScopeClusterID     string
	Source             string
	AliasType          string
	AliasValue         string
	AliasValueSHA256   string
	CanonicalEntityUID string
	Confidence         float64
	Status             string
	Resolver           string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
}

type GraphEntityAliasDAO struct{}

func (d *GraphEntityAliasDAO) ListByTenant(tenantID string, limit int) ([]GraphEntityAlias, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT alias_id, tenant_id, scope_cluster_id, source, alias_type, alias_value,
    alias_value_sha256, canonical_entity_uid, confidence, status, resolver, first_seen_at, last_seen_at
    FROM graph_entity_alias WHERE tenant_id=? ORDER BY updated_at DESC, alias_id DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GraphEntityAlias, 0, limit)
	for rows.Next() {
		var item GraphEntityAlias
		var firstSeen, lastSeen sql.NullTime
		if err := rows.Scan(&item.AliasID, &item.TenantID, &item.ScopeClusterID, &item.Source, &item.AliasType, &item.AliasValue,
			&item.AliasValueSHA256, &item.CanonicalEntityUID, &item.Confidence, &item.Status, &item.Resolver, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		if firstSeen.Valid {
			item.FirstSeenAt = firstSeen.Time
		}
		if lastSeen.Valid {
			item.LastSeenAt = lastSeen.Time
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *GraphEntityAliasDAO) ResolveConflict(aliasID int64, canonicalEntityUID string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if aliasID <= 0 || canonicalEntityUID == "" {
		return errors.New("alias id and canonical entity uid are required")
	}
	_, err := conn.Exec(`UPDATE graph_entity_alias SET canonical_entity_uid=?, status='active', resolver='admin', last_seen_at=NOW()
    WHERE alias_id=? AND status='conflict'`, canonicalEntityUID, aliasID)
	return err
}

func (d *GraphEntityAliasDAO) Upsert(alias GraphEntityAlias) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if alias.AliasValueSHA256 == "" {
		alias.AliasValueSHA256 = sha256Parts(alias.AliasValue)
	}
	if alias.Status == "" {
		alias.Status = "active"
	}
	if alias.Resolver == "" {
		alias.Resolver = "deterministic"
	}
	if alias.Confidence == 0 {
		alias.Confidence = 1
	}
	_, err := conn.Exec(`INSERT INTO graph_entity_alias
    (tenant_id, scope_cluster_id, source, alias_type, alias_value, alias_value_sha256,
     canonical_entity_uid, confidence, status, resolver)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE canonical_entity_uid=canonical_entity_uid,
		  confidence=VALUES(confidence), status=IF(canonical_entity_uid=VALUES(canonical_entity_uid), VALUES(status), 'conflict'),
		  resolver=VALUES(resolver), last_seen_at=NOW()`,
		alias.TenantID, alias.ScopeClusterID, alias.Source, alias.AliasType, alias.AliasValue,
		alias.AliasValueSHA256, alias.CanonicalEntityUID, alias.Confidence, alias.Status, alias.Resolver)
	return err
}

// UpsertMany atomically writes a bounded batch of canonical name aliases.
//
// The Query service owns graph_entity_alias, including validation tools that
// seed a local rebuild. Keeping the write in this DAO prevents validation
// utilities from opening a second data-owner implementation or issuing
// per-row autocommit statements. Callers must chunk batches to 500 rows.
func (d *GraphEntityAliasDAO) UpsertMany(aliases []GraphEntityAlias) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if len(aliases) == 0 {
		return nil
	}
	if len(aliases) > 500 {
		return errors.New("graph alias batch exceeds 500 rows")
	}

	placeholders := make([]string, 0, len(aliases))
	args := make([]interface{}, 0, len(aliases)*10)
	for _, alias := range aliases {
		if alias.TenantID == "" || alias.ScopeClusterID == "" || alias.Source == "" ||
			alias.AliasType == "" || alias.AliasValue == "" || alias.CanonicalEntityUID == "" {
			return errors.New("graph alias requires tenant, cluster, source, type, value and canonical uid")
		}
		if alias.AliasValueSHA256 == "" {
			alias.AliasValueSHA256 = sha256Parts(alias.AliasValue)
		}
		if alias.Status == "" {
			alias.Status = "active"
		}
		if alias.Resolver == "" {
			alias.Resolver = "deterministic"
		}
		if alias.Confidence == 0 {
			alias.Confidence = 1
		}
		placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args, alias.TenantID, alias.ScopeClusterID, alias.Source, alias.AliasType,
			alias.AliasValue, alias.AliasValueSHA256, alias.CanonicalEntityUID, alias.Confidence,
			alias.Status, alias.Resolver)
	}

	query := `INSERT INTO graph_entity_alias
    (tenant_id, scope_cluster_id, source, alias_type, alias_value, alias_value_sha256,
     canonical_entity_uid, confidence, status, resolver)
    VALUES ` + strings.Join(placeholders, ", ") + `
    ON DUPLICATE KEY UPDATE canonical_entity_uid=canonical_entity_uid,
      confidence=VALUES(confidence), status=IF(canonical_entity_uid=VALUES(canonical_entity_uid), VALUES(status), 'conflict'),
      resolver=VALUES(resolver), last_seen_at=NOW()`
	const maxAttempts = 4
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(query, args...); err != nil {
			_ = tx.Rollback()
			if isRetryableGraphAliasTxError(err) && attempt < maxAttempts {
				time.Sleep(time.Duration(attempt*25) * time.Millisecond)
				continue
			}
			return fmt.Errorf("upsert graph aliases: %w", err)
		}
		if err = tx.Commit(); err != nil {
			if isRetryableGraphAliasTxError(err) && attempt < maxAttempts {
				time.Sleep(time.Duration(attempt*25) * time.Millisecond)
				continue
			}
			return fmt.Errorf("commit graph aliases: %w", err)
		}
		return nil
	}
	return errors.New("upsert graph aliases exhausted retry budget")
}

func isRetryableGraphAliasTxError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1205 || mysqlErr.Number == 1213
	}
	return false
}

// Search resolves normalized name aliases. It is intentionally the only
// prefix-search primitive used by the public graph API; HugeGraph is never
// scanned by display name.
func (d *GraphEntityAliasDAO) Search(tenantID, scopeClusterID, aliasValue string, limit int) ([]GraphEntityAlias, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := conn.Query(`SELECT alias_id, tenant_id, scope_cluster_id, source, alias_type, alias_value,
    alias_value_sha256, canonical_entity_uid, confidence, status, resolver, first_seen_at, last_seen_at
    FROM graph_entity_alias WHERE tenant_id=? AND scope_cluster_id=? AND alias_type='name'
      AND alias_value LIKE ? AND status IN ('active','conflict') ORDER BY alias_value, alias_id LIMIT ?`,
		tenantID, scopeClusterID, aliasValue+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GraphEntityAlias, 0, limit)
	for rows.Next() {
		var item GraphEntityAlias
		var firstSeen, lastSeen sql.NullTime
		if err := rows.Scan(&item.AliasID, &item.TenantID, &item.ScopeClusterID, &item.Source, &item.AliasType, &item.AliasValue,
			&item.AliasValueSHA256, &item.CanonicalEntityUID, &item.Confidence, &item.Status, &item.Resolver, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		if firstSeen.Valid {
			item.FirstSeenAt = firstSeen.Time
		}
		if lastSeen.Valid {
			item.LastSeenAt = lastSeen.Time
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *GraphEntityAliasDAO) Resolve(tenantID, scopeClusterID, aliasType, aliasValue string) (*GraphEntityAlias, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var out GraphEntityAlias
	err := conn.QueryRow(`SELECT alias_id, tenant_id, scope_cluster_id, source, alias_type, alias_value,
    alias_value_sha256, canonical_entity_uid, confidence, status, resolver, first_seen_at, last_seen_at
    FROM graph_entity_alias WHERE tenant_id=? AND scope_cluster_id=? AND alias_type=? AND alias_value_sha256=?
      AND status='active' LIMIT 1`, tenantID, scopeClusterID, aliasType, sha256Parts(aliasValue)).Scan(
		&out.AliasID, &out.TenantID, &out.ScopeClusterID, &out.Source, &out.AliasType, &out.AliasValue,
		&out.AliasValueSHA256, &out.CanonicalEntityUID, &out.Confidence, &out.Status, &out.Resolver,
		&out.FirstSeenAt, &out.LastSeenAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func sha256Parts(parts ...string) string {
	data := make([]byte, 0)
	for i, part := range parts {
		if i > 0 {
			data = append(data, 0x1f)
		}
		data = append(data, []byte(part)...)
	}
	return sha256HexBytes(data)
}
