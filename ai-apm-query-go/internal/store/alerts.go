package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─── AlertRule ───────────────────────────────────────────────

// AlertRule 数据库实体。
type AlertRule struct {
	ID              string
	Name            string
	Service         string
	Type            string
	Metric          string
	Condition       string
	Threshold       float64
	Duration        int
	Severity        string
	Enabled         bool
	WebhookURL      string
	Cooldown        int
	Dampening       int
	BaselineSeconds int    // anomaly 基线窗口（秒）
	AnomalyMethod   string // anomaly 检测方法：zscore|mad
	SLOID           string // burn_rate 引用的 SLO 目标 id
}

// AlertRuleDAO 告警规则数据访问（全量重建 + 加载）。
type AlertRuleDAO struct{}

// LoadAll 读取全部规则（无数据返回空 slice）。
func (d *AlertRuleDAO) LoadAll() ([]AlertRule, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		"SELECT id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url, cooldown, dampening, baseline_seconds, anomaly_method, slo_id FROM alert_rules")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		var en int
		var wh sql.NullString
		var am sql.NullString
		var sloID sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Service, &r.Type, &r.Metric,
			&r.Condition, &r.Threshold, &r.Duration, &r.Severity, &en, &wh, &r.Cooldown, &r.Dampening,
			&r.BaselineSeconds, &am, &sloID); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		r.WebhookURL = wh.String
		r.AnomalyMethod = am.String
		r.SLOID = sloID.String
		out = append(out, r)
	}
	return out, nil
}

// ReplaceAll 全量同步规则表：逐行 upsert（ON DUPLICATE KEY UPDATE）避免 DELETE+INSERT 两阶段，
// 再删除不在当前列表中的行。事务内保证原子性，多副本下不会互相清空。
func (d *AlertRuleDAO) ReplaceAll(rules []AlertRule) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(
		"INSERT INTO alert_rules (id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url, cooldown, dampening, baseline_seconds, anomaly_method, slo_id) " +
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) " +
			"ON DUPLICATE KEY UPDATE name=VALUES(name), service=VALUES(service), type=VALUES(type), metric=VALUES(metric), " +
			"cond=VALUES(cond), threshold=VALUES(threshold), duration=VALUES(duration), severity=VALUES(severity), enabled=VALUES(enabled), webhook_url=VALUES(webhook_url), cooldown=VALUES(cooldown), dampening=VALUES(dampening), " +
			"baseline_seconds=VALUES(baseline_seconds), anomaly_method=VALUES(anomaly_method), slo_id=VALUES(slo_id)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	ids := make(map[string]bool, len(rules))
	for _, r := range rules {
		ids[r.ID] = true
		if _, err := stmt.Exec(r.ID, r.Name, r.Service, r.Type, r.Metric,
			r.Condition, r.Threshold, r.Duration, r.Severity, boolToInt(r.Enabled), r.WebhookURL, r.Cooldown, r.Dampening,
			r.BaselineSeconds, r.AnomalyMethod, r.SLOID); err != nil {
			return err
		}
	}
	// 删除不在当前列表中的行（ID 不在 ids 中）
	if len(ids) > 0 {
		delIDs := make([]interface{}, 0, len(ids))
		placeholders := ""
		for id := range ids {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			delIDs = append(delIDs, id)
		}
		if _, err := tx.Exec("DELETE FROM alert_rules WHERE id NOT IN ("+placeholders+")", delIDs...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ─── AlertEvent ──────────────────────────────────────────────

// AlertEvent 数据库实体。
type AlertEvent struct {
	ID              string
	RuleID          string
	RuleName        string
	Service         string
	Severity        string
	Message         string
	Value           float64
	Threshold       float64
	Timestamp       string
	Count           int
	FirstTimestamp  string
	LastTimestamp   string
	Status          string
	AcknowledgedAt  string
	AcknowledgedBy  string
	ResolvedAt      string
	ResolvedBy      string
	Timeline        string
	Investigation   string
	Signature       string
}

// AlertEventDAO 告警事件数据访问。
type AlertEventDAO struct{}

// LoadAll 读取全部事件。
func (d *AlertEventDAO) LoadAll() ([]AlertEvent, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT id, rule_id, rule_name, service, severity, message, value, threshold,
		        timestamp, count, first_timestamp, last_timestamp, status,
		        acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline, investigation, signature
		 FROM alert_events`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		var e AlertEvent
		var ts, fts, lts, aat, rat, tl, inv, sig sql.NullString
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.Service, &e.Severity,
			&e.Message, &e.Value, &e.Threshold, &ts, &e.Count, &fts, &lts, &e.Status,
			&aat, &e.AcknowledgedBy, &rat, &e.ResolvedBy, &tl, &inv, &sig); err != nil {
			return nil, err
		}
		e.Timestamp = ts.String
		e.FirstTimestamp = fts.String
		e.LastTimestamp = lts.String
		e.AcknowledgedAt = aat.String
		e.ResolvedAt = rat.String
		e.Timeline = tl.String
		e.Investigation = inv.String
		e.Signature = sig.String
		out = append(out, e)
	}
	return out, nil
}

// ReplaceAll 全量同步事件表：逐行 upsert（避免 DELETE+INSERT），再删除不在列表中的行。
func (d *AlertEventDAO) ReplaceAll(events []AlertEvent) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(
		`INSERT INTO alert_events (id, rule_id, rule_name, service, severity, message, value, threshold,
		        timestamp, count, first_timestamp, last_timestamp, status,
		        acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline, investigation, signature)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE rule_id=VALUES(rule_id), rule_name=VALUES(rule_name), service=VALUES(service),
		   severity=VALUES(severity), message=VALUES(message), value=VALUES(value), threshold=VALUES(threshold),
		   timestamp=VALUES(timestamp), count=VALUES(count), first_timestamp=VALUES(first_timestamp),
		   last_timestamp=VALUES(last_timestamp), status=VALUES(status), acknowledged_at=VALUES(acknowledged_at),
		   acknowledged_by=VALUES(acknowledged_by), resolved_at=VALUES(resolved_at), resolved_by=VALUES(resolved_by),
		   timeline=VALUES(timeline), investigation=VALUES(investigation), signature=VALUES(signature)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	ids := make(map[string]bool, len(events))
	for _, e := range events {
		ids[e.ID] = true
		if _, err := stmt.Exec(e.ID, e.RuleID, e.RuleName, e.Service, e.Severity,
			e.Message, e.Value, e.Threshold, nullStr(e.Timestamp), e.Count,
			nullStr(e.FirstTimestamp), nullStr(e.LastTimestamp), e.Status,
			nullStr(e.AcknowledgedAt), e.AcknowledgedBy, nullStr(e.ResolvedAt), e.ResolvedBy,
			nullStr(e.Timeline), nullStr(e.Investigation), nullStr(e.Signature)); err != nil {
			return err
		}
	}
	// 删除不在当前列表中的行（保留最多 N 条，防止无限增长）
	if len(ids) > 0 {
		delIDs := make([]interface{}, 0, len(ids))
		placeholders := ""
		for id := range ids {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			delIDs = append(delIDs, id)
		}
		if _, err := tx.Exec("DELETE FROM alert_events WHERE id NOT IN ("+placeholders+")", delIDs...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ─── AlertSilence ────────────────────────────────────────────

// AlertSilence 数据库实体。
type AlertSilence struct {
	ID        string
	Service   string
	RuleID    string
	Comment   string
	CreatedAt string
	ExpiresAt string
}

// AlertSilenceDAO 告警静默数据访问。
type AlertSilenceDAO struct{}

// LoadAll 读取全部静默。
func (d *AlertSilenceDAO) LoadAll() ([]AlertSilence, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		"SELECT id, service, rule_id, comment, created_at, expires_at FROM alert_silences")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertSilence{}
	for rows.Next() {
		var s AlertSilence
		var ca, ea sql.NullString
		if err := rows.Scan(&s.ID, &s.Service, &s.RuleID, &s.Comment, &ca, &ea); err != nil {
			return nil, err
		}
		s.CreatedAt = ca.String
		s.ExpiresAt = ea.String
		out = append(out, s)
	}
	return out, nil
}

// ReplaceAll 全量同步静默表：逐行 upsert + 删除差异行。
func (d *AlertSilenceDAO) ReplaceAll(silences []AlertSilence) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(
		"INSERT INTO alert_silences (id, service, rule_id, comment, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?) " +
			"ON DUPLICATE KEY UPDATE service=VALUES(service), rule_id=VALUES(rule_id), comment=VALUES(comment), " +
			"created_at=VALUES(created_at), expires_at=VALUES(expires_at)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	ids := make(map[string]bool, len(silences))
	for _, s := range silences {
		ids[s.ID] = true
		if _, err := stmt.Exec(s.ID, s.Service, s.RuleID, s.Comment,
			nullStr(s.CreatedAt), nullStr(s.ExpiresAt)); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		delIDs := make([]interface{}, 0, len(ids))
		placeholders := ""
		for id := range ids {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			delIDs = append(delIDs, id)
		}
		if _, err := tx.Exec("DELETE FROM alert_silences WHERE id NOT IN ("+placeholders+")", delIDs...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ─── Tenant ──────────────────────────────────────────────────

// Tenant 数据库实体。
type Tenant struct {
	ID      string
	Name    string
	QuotaAI int
	Enabled bool
}

// TenantDAO 租户数据访问。
type TenantDAO struct{}

// LoadAll 读取全部租户。
func (d *TenantDAO) LoadAll() ([]Tenant, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT id, name, quota_ai, enabled FROM tenants")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var t Tenant
		var en int
		if err := rows.Scan(&t.ID, &t.Name, &t.QuotaAI, &en); err != nil {
			return nil, err
		}
		t.Enabled = en == 1
		out = append(out, t)
	}
	return out, nil
}

// ReplaceAll 全量重建租户表。
func (d *TenantDAO) ReplaceAll(tenants []Tenant) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM tenants"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		"INSERT INTO tenants (id, name, quota_ai, enabled) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, t := range tenants {
		if _, err := stmt.Exec(t.ID, t.Name, t.QuotaAI, boolToInt(t.Enabled)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ─── helpers ─────────────────────────────────────────────────

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

var _ = time.Now
