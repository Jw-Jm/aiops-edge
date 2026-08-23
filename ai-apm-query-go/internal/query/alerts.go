package query

import (
	"context"
	"fmt"
)

// AlertEvent 一条告警事件（query 层的规范化结构）。
type AlertEvent struct {
	ID            string
	RuleID        string
	RuleName      string
	Service       string
	Severity      string
	Message       string
	Value         string
	Threshold     string
	Timestamp     string
	Count         string
	FirstTS       string
	LastTS        string
	Status        string
	Acknowledged  string
	ResolvedAt    string
	ResolvedBy    string
	ClusterID     string
	Signature     string
	Extra         map[string]interface{}
}

// AlertRepository 是 alerts 资源域的 domain repository（V9.2 Phase 6）。
// alert_events SoT 在 ClickHouse（冻结职责），无 SoT 切换。
type AlertRepository struct {
	ch *ClickHouseRepo
}

// NewAlertRepository 构造 alerts repository。
func NewAlertRepository(ch *ClickHouseRepo) *AlertRepository {
	return &AlertRepository{ch: ch}
}

// ListEvents 查询告警事件（按 last_timestamp 倒序，分页/服务过滤）。
func (r *AlertRepository) ListEvents(ctx context.Context, service string, limit, offset int) ([]AlertEvent, error) {
	where := ""
	if service != "" {
		where = " WHERE service = '" + service + "'"
	}
	if limit <= 0 {
		limit = 100
	}
	sql := "SELECT id, rule_id, rule_name, service, severity, message, value, threshold, timestamp, count, " +
		"first_timestamp, last_timestamp, status, acknowledged_at, acknowledged_by, resolved_at, resolved_by, " +
		"timeline, investigation, signature, cluster_id FROM observability.alert_events FINAL" + where +
		fmt.Sprintf(" ORDER BY last_timestamp DESC LIMIT %d OFFSET %d", limit, offset)

	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]AlertEvent, 0, len(rows))
	for _, row := range rows {
		e := AlertEvent{
			ID:           str(row, "id"),
			RuleID:       str(row, "rule_id"),
			RuleName:     str(row, "rule_name"),
			Service:      str(row, "service"),
			Severity:     str(row, "severity"),
			Message:      str(row, "message"),
			Value:        str(row, "value"),
			Threshold:    str(row, "threshold"),
			Timestamp:    str(row, "timestamp"),
			Count:        str(row, "count"),
			FirstTS:      str(row, "first_timestamp"),
			LastTS:       str(row, "last_timestamp"),
			Status:       str(row, "status"),
			Acknowledged: str(row, "acknowledged_at"),
			ResolvedAt:   str(row, "resolved_at"),
			ResolvedBy:   str(row, "resolved_by"),
			ClusterID:    str(row, "cluster_id"),
			Signature:    str(row, "signature"),
			Extra:        row,
		}
		out = append(out, e)
	}
	return out, nil
}

// str 安全取 map 中的字符串值（ClickHouse JSON 数值可能是 float64/string）。
func str(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}
