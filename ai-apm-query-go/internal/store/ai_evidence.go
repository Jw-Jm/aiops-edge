package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 27.14 Evidence 一次消费：ToolRun → ai_evidence 原子创建 + evidence_consumed_at 标记。
//
// 条件（同事务，防重复转 Evidence）：
//   - ToolRun status 为终态成功（success/partial/no_data as allowed）；
//   - eligible_for_evidence=true；
//   - evidence_consumed_at IS NULL（未被消费过）；
//   - 同 run/tenant/cluster（不跨租户/集群）。
//
// A-C 超过 11.10 固定上限时只允许服务端 deterministic truncation 或 RESULT_TOO_LARGE，
// 不隐式上传 MinIO/Object Storage（Object First 属后续 StorageAdapter 阶段）。
// ─────────────────────────────────────────────────────────────────────────────

// Evidence 是对应 ai_evidence 表的记录（原子创建时写入）。
type Evidence struct {
	EvidenceID            string
	RunID                 string
	TenantID              string
	ClusterID             string
	EvidenceType          string
	SourceRef             string
	RawRef                string
	RawDigestSHA256       string
	Summary               string
	MetadataJSON          []byte
	ProvenanceFingerprint string
	CollectedAt           time.Time
}

// EvidenceDAO 访问 ai_evidence 表。
type EvidenceDAO struct{}

// ListByRun returns persisted Evidence for a Run. Scope filtering is included
// in the query so callers cannot accidentally mix tenants/clusters.
func (d *EvidenceDAO) ListByRun(runID, tenantID, clusterID string) ([]Evidence, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(`SELECT evidence_id, run_id, tenant_id, cluster_id,
		evidence_type, source_ref, raw_ref, raw_digest_sha256, summary, metadata_json,
		provenance_fingerprint, collected_at
		FROM ai_evidence WHERE run_id = ? AND tenant_id = ? AND cluster_id = ? ORDER BY collected_at ASC`,
		runID, tenantID, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		var ev Evidence
		var summary sql.NullString
		if err := rows.Scan(&ev.EvidenceID, &ev.RunID, &ev.TenantID, &ev.ClusterID,
			&ev.EvidenceType, &ev.SourceRef, &ev.RawRef, &ev.RawDigestSHA256,
			&summary, &ev.MetadataJSON, &ev.ProvenanceFingerprint, &ev.CollectedAt); err != nil {
			return nil, err
		}
		ev.Summary = summary.String
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (d *EvidenceDAO) GetByID(evidenceID, runID, tenantID, clusterID string) (*Evidence, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var ev Evidence
	var summary sql.NullString
	err := conn.QueryRow(`SELECT evidence_id, run_id, tenant_id, cluster_id,
		evidence_type, source_ref, raw_ref, raw_digest_sha256, summary, metadata_json,
		provenance_fingerprint, collected_at FROM ai_evidence
		WHERE evidence_id = ? AND run_id = ? AND tenant_id = ? AND cluster_id = ?`,
		evidenceID, runID, tenantID, clusterID).Scan(
		&ev.EvidenceID, &ev.RunID, &ev.TenantID, &ev.ClusterID,
		&ev.EvidenceType, &ev.SourceRef, &ev.RawRef, &ev.RawDigestSHA256,
		&summary, &ev.MetadataJSON, &ev.ProvenanceFingerprint, &ev.CollectedAt)
	if err != nil {
		return nil, err
	}
	ev.Summary = summary.String
	return &ev, nil
}

// ErrEvidenceNotEligible 表示 ToolRun 不满足进入 Evidence 的条件（跨 epoch/终态/已消费/不匹配）。
var ErrEvidenceNotEligible = errors.New("toolrun not eligible for evidence")

// ConsumeToolRunAsEvidence 同事务消费一个 ToolRun 生成 Evidence：
// 锁定并 recheck ToolRun 条件（终态成功 + eligible + 未消费 + 同 run/tenant/cluster），
// 满足则创建 ai_evidence 并同事务设置 evidence_consumed_at=DB_NOW。
// 返回 true 表示成功创建；false 表示不满足条件（不伪装）。
func (d *EvidenceDAO) ConsumeToolRunAsEvidence(tx *sql.Tx, ev Evidence, toolRunID, allowedStatuses string) (bool, error) {
	var status string
	var eligible int
	var consumed sql.NullTime
	var trRun, trTenant, trCluster string
	err := tx.QueryRow(
		`SELECT status, eligible_for_evidence, evidence_consumed_at, run_id, tenant_id, cluster_id
		   FROM ai_tool_runs WHERE tool_run_id = ? FOR UPDATE`, toolRunID,
	).Scan(&status, &eligible, &consumed, &trRun, &trTenant, &trCluster)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrEvidenceNotEligible
		}
		return false, err
	}
	// 条件：终态成功 + eligible + 未消费 + 同 run/tenant/cluster（不跨 epoch/终态进入 Evidence）。
	if eligible != 1 || consumed.Valid {
		return false, ErrEvidenceNotEligible
	}
	if !statusIn(status, allowedStatuses) {
		return false, ErrEvidenceNotEligible
	}
	if trRun != ev.RunID || trTenant != ev.TenantID || trCluster != ev.ClusterID {
		return false, ErrEvidenceNotEligible
	}
	// 创建 ai_evidence
	if _, err := tx.Exec(
		`INSERT INTO ai_evidence (evidence_id, run_id, tenant_id, cluster_id, evidence_type,
		   source_ref, raw_ref, raw_digest_sha256, summary, metadata_json,
		   provenance_fingerprint, collected_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE provenance_fingerprint = VALUES(provenance_fingerprint)`,
		ev.EvidenceID, ev.RunID, ev.TenantID, ev.ClusterID, ev.EvidenceType,
		ev.SourceRef, ev.RawRef, ev.RawDigestSHA256, nullableStr(ev.Summary),
		ev.MetadataJSON, ev.ProvenanceFingerprint, ev.CollectedAt, time.Now(),
	); err != nil {
		return false, err
	}
	// 同事务标记已消费（防重复转 Evidence）
	if _, err := tx.Exec(
		`UPDATE ai_tool_runs SET evidence_consumed_at = ? WHERE tool_run_id = ? AND evidence_consumed_at IS NULL`,
		time.Now(), toolRunID,
	); err != nil {
		return false, err
	}
	return true, nil
}

func statusIn(s, list string) bool {
	// list 逗号分隔；空视为不放行（fail-closed）
	if list == "" {
		return false
	}
	for _, item := range splitComma(list) {
		if item == s {
			return true
		}
	}
	return false
}

func splitComma(s string) []string {
	out := []string{}
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else {
			cur += string(s[i])
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
