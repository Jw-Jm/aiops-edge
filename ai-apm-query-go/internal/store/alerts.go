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
	Keyword         string // log_keyword 日志关键字（body LIKE '%keyword%'）
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
		"SELECT id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url, cooldown, dampening, baseline_seconds, anomaly_method, slo_id, keyword FROM alert_rules")
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
		var kw sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Service, &r.Type, &r.Metric,
			&r.Condition, &r.Threshold, &r.Duration, &r.Severity, &en, &wh, &r.Cooldown, &r.Dampening,
			&r.BaselineSeconds, &am, &sloID, &kw); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		r.WebhookURL = wh.String
		r.AnomalyMethod = am.String
		r.SLOID = sloID.String
		r.Keyword = kw.String
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
		"INSERT INTO alert_rules (id, name, service, type, metric, cond, threshold, duration, severity, enabled, webhook_url, cooldown, dampening, baseline_seconds, anomaly_method, slo_id, keyword) " +
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) " +
			"ON DUPLICATE KEY UPDATE name=VALUES(name), service=VALUES(service), type=VALUES(type), metric=VALUES(metric), " +
			"cond=VALUES(cond), threshold=VALUES(threshold), duration=VALUES(duration), severity=VALUES(severity), enabled=VALUES(enabled), webhook_url=VALUES(webhook_url), cooldown=VALUES(cooldown), dampening=VALUES(dampening), " +
			"baseline_seconds=VALUES(baseline_seconds), anomaly_method=VALUES(anomaly_method), slo_id=VALUES(slo_id), keyword=VALUES(keyword)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	ids := make(map[string]bool, len(rules))
	for _, r := range rules {
		ids[r.ID] = true
		if _, err := stmt.Exec(r.ID, r.Name, r.Service, r.Type, r.Metric,
			r.Condition, r.Threshold, r.Duration, r.Severity, boolToInt(r.Enabled), r.WebhookURL, r.Cooldown, r.Dampening,
			r.BaselineSeconds, r.AnomalyMethod, r.SLOID, r.Keyword); err != nil {
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
// 告警事件已迁移到 ClickHouse（observability.alert_events，ReplacingMergeTree + TTL），
// MySQL 侧不再维护 alert_events 表（历史数据可清理，见 init_clickhouse.sql）。

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

// mysqlTime 把 RFC3339（如 2026-08-09T04:28:59Z）转成 MySQL datetime 格式（2026-08-09 04:28:59）。
// 解析失败或空串原样返回，由 nullStr 处理空串。
func mysqlTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format("2006-01-02 15:04:05")
}

var _ = time.Now
