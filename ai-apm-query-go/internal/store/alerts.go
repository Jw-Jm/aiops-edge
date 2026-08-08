package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─── AlertRule ───────────────────────────────────────────────

// AlertRule 数据库实体。
type AlertRule struct {
	ID         string
	Name       string
	Service    string
	Type       string
	Metric     string
	Condition  string
	Threshold  float64
	Duration   int
	Severity   string
	Enabled    bool
	WebhookURL string
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
		"SELECT id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url FROM alert_rules")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		var en int
		var wh sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Service, &r.Type, &r.Metric,
			&r.Condition, &r.Threshold, &r.Duration, &r.Severity, &en, &wh); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		r.WebhookURL = wh.String
		out = append(out, r)
	}
	return out, nil
}

// ReplaceAll 全量重建规则表（事务内 DELETE + INSERT）。
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
	if _, err := tx.Exec("DELETE FROM alert_rules"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		"INSERT INTO alert_rules (id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rules {
		if _, err := stmt.Exec(r.ID, r.Name, r.Service, r.Type, r.Metric,
			r.Condition, r.Threshold, r.Duration, r.Severity, boolToInt(r.Enabled), r.WebhookURL); err != nil {
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
		        acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline
		 FROM alert_events`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		var e AlertEvent
		var ts, fts, lts, aat, rat, tl sql.NullString
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.Service, &e.Severity,
			&e.Message, &e.Value, &e.Threshold, &ts, &e.Count, &fts, &lts, &e.Status,
			&aat, &e.AcknowledgedBy, &rat, &e.ResolvedBy, &tl); err != nil {
			return nil, err
		}
		e.Timestamp = ts.String
		e.FirstTimestamp = fts.String
		e.LastTimestamp = lts.String
		e.AcknowledgedAt = aat.String
		e.ResolvedAt = rat.String
		e.Timeline = tl.String
		out = append(out, e)
	}
	return out, nil
}

// ReplaceAll 全量重建事件表（事务内 DELETE + INSERT）。
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
	if _, err := tx.Exec("DELETE FROM alert_events"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO alert_events (id, rule_id, rule_name, service, severity, message, value, threshold,
		        timestamp, count, first_timestamp, last_timestamp, status,
		        acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range events {
		if _, err := stmt.Exec(e.ID, e.RuleID, e.RuleName, e.Service, e.Severity,
			e.Message, e.Value, e.Threshold, nullStr(e.Timestamp), e.Count,
			nullStr(e.FirstTimestamp), nullStr(e.LastTimestamp), e.Status,
			nullStr(e.AcknowledgedAt), e.AcknowledgedBy, nullStr(e.ResolvedAt), e.ResolvedBy,
			nullStr(e.Timeline)); err != nil {
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

// ReplaceAll 全量重建静默表。
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
	if _, err := tx.Exec("DELETE FROM alert_silences"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		"INSERT INTO alert_silences (id, service, rule_id, comment, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range silences {
		if _, err := stmt.Exec(s.ID, s.Service, s.RuleID, s.Comment,
			nullStr(s.CreatedAt), nullStr(s.ExpiresAt)); err != nil {
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
