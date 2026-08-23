package store

import (
	"database/sql"
)

// EnsureBootstrapData 幂等地写入 bootstrap seed DML（V9.2 Phase 4 P4.4）。
//
// 这是 DML-only：绝不执行 CREATE/ALTER/DROP/INDEX，因此可用 aiops_app 账号运行。
// 所有 DDL 与一次性 backfill 已迁入 versioned migration（schema-migrator 执行）。
// 本函数只负责"合法启动 seed"（初始默认面板等幂等数据）。
//
// 原则（用户 P4.4 拍板）：一次性历史数据修正 → migration；正常业务运行 DML → runtime。
func EnsureBootstrapData(conn *sql.DB) error {
	if conn == nil {
		return nil
	}
	if err := seedDefaultDashboardPanels(conn); err != nil {
		return err
	}
	return nil
}

// seedDefaultDashboardPanels 首次初始化默认看板面板（幂等：仅当面板表为空时写入）。
func seedDefaultDashboardPanels(conn *sql.DB) error {
	var count int
	if err := conn.QueryRow("SELECT count(*) FROM dashboard_panels").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	seedPanels := []struct {
		id, title, query, chart string
		span                    int
	}{
		{"panel-1", "服务请求速率", "sum(rate(http_requests_total[5m])) by (service)", "line", 6},
		{"panel-2", "服务错误率", "sum(rate(http_requests_total{status=~\"5..\"}[5m])) by (service)", "line", 6},
		{"panel-3", "延迟 P95", "histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, service))", "line", 6},
		{"panel-4", "CPU 使用率", "100 - avg(rate(node_cpu_seconds_total{mode=\"idle\"}[5m])) * 100", "line", 6},
	}
	for i, sp := range seedPanels {
		if _, err := conn.Exec(
			"INSERT IGNORE INTO dashboard_panels (id, title, query, chart_type, span, sort, enabled) VALUES (?, ?, ?, ?, ?, ?, 1)",
			sp.id, sp.title, sp.query, sp.chart, sp.span, i); err != nil {
			return err
		}
	}
	return nil
}
