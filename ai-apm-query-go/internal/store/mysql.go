// Package store 提供 query-api 的 MySQL 持久化（users 表）。
// 采用 database/sql + go-sql-driver/mysql 轻量 DAO，无 ORM。
// MySQL 不可达时 GetDB() 返回 nil，调用方降级处理（不阻塞）。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"sync"

	_ "github.com/go-sql-driver/mysql"
)

var (
	dbOnce sync.Once
	db     *sql.DB
)

// GetDB 返回全局 MySQL 连接池；不可达时返回 nil。
func GetDB() *sql.DB {
	dbOnce.Do(func() {
		host := env("MYSQL_HOST", "127.0.0.1")
		port := env("MYSQL_PORT", "3306")
		user := env("MYSQL_USER", "root")
		pw := env("MYSQL_PASSWORD", "")
		database := env("MYSQL_DB", "aiops")
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true",
			user, pw, host, port, database)
		conn, err := sql.Open("mysql", dsn)
		if err != nil {
			return
		}
		conn.SetMaxOpenConns(10)
		conn.SetMaxIdleConns(5)
		conn.SetConnMaxLifetime(0)
		if err := conn.Ping(); err != nil {
			conn.Close()
			return
		}
		db = conn
	})
	return db
}

// hasColumn 检查表是否存在指定列（幂等迁移辅助）。
func hasColumn(conn *sql.DB, table, column string) bool {
	if conn == nil {
		return false
	}
	rows, err := conn.Query(
		"SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?",
		table, column)
	if err != nil {
		return false
	}
	defer rows.Close()
	return rows.Next()
}

// EnsureSchema 应用 users 表迁移并种子 admin 用户（幂等）。MySQL 不可达时静默。
func EnsureSchema() {
	conn := GetDB()
	if conn == nil {
		return
	}
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(128) DEFAULT '',
  role ENUM('admin','user') NOT NULL DEFAULT 'user',
  email VARCHAR(128) DEFAULT '',
  status TINYINT DEFAULT 1,
  scope VARCHAR(512) DEFAULT '',
  is_approver TINYINT DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// 兼容已存在的 users 表：补 scope 列（幂等）
	hasScope := hasColumn(conn, "users", "scope")
	if !hasScope {
		_, _ = conn.Exec("ALTER TABLE users ADD COLUMN scope VARCHAR(512) DEFAULT ''")
	}
	// 兼容已存在的 users 表：补 is_approver 列（幂等）
	if !hasColumn(conn, "users", "is_approver") {
		_, _ = conn.Exec("ALTER TABLE users ADD COLUMN is_approver TINYINT DEFAULT 0")
	}

	// service_catalog 服务目录
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS service_catalog (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  service_name VARCHAR(128) NOT NULL UNIQUE,
  display_name VARCHAR(128) DEFAULT '',
  description TEXT,
  owner VARCHAR(128) DEFAULT '',
  team VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  status ENUM('active','maintenance','deprecated') DEFAULT 'active',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// devices 设备
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS devices (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  hostname VARCHAR(128) NOT NULL UNIQUE,
  ip VARCHAR(64) DEFAULT '',
  os VARCHAR(64) DEFAULT '',
  cpu_cores INT DEFAULT 0,
  memory_mb BIGINT DEFAULT 0,
  status ENUM('online','offline','maintenance') DEFAULT 'online',
  role VARCHAR(64) DEFAULT '',
  location VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// clusters 集群
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS clusters (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL UNIQUE,
  provider VARCHAR(64) DEFAULT '',
  region VARCHAR(64) DEFAULT '',
  version VARCHAR(64) DEFAULT '',
  node_count INT DEFAULT 0,
  status ENUM('active','degraded','down') DEFAULT 'active',
  api_server VARCHAR(255) DEFAULT '',
  kubeconfig TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// 兼容已存在的 clusters 表：补 kubeconfig 列（幂等）
	if !hasColumn(conn, "clusters", "kubeconfig") {
		_, _ = conn.Exec("ALTER TABLE clusters ADD COLUMN kubeconfig TEXT")
	}

	// topology_nodes 拓扑顶点（typed property graph）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS topology_nodes (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  type VARCHAR(32) NOT NULL,
  name VARCHAR(255) NOT NULL,
  props_json TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_nodes_type_name (type, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// topology_relations 拓扑有向边（src→dst, type 唯一）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS topology_relations (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  src_id BIGINT NOT NULL,
  dst_id BIGINT NOT NULL,
  type VARCHAR(64) NOT NULL,
  props_json TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_relations_src_dst_type (src_id, dst_id, type),
  KEY idx_relations_src_type (src_id, type),
  KEY idx_relations_dst_type (dst_id, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// topology_node_types 节点类型目录
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS topology_node_types (
  name VARCHAR(32) PRIMARY KEY,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  display_name_en VARCHAR(128) NOT NULL DEFAULT '',
  builtin TINYINT NOT NULL DEFAULT 0,
  tier INT NOT NULL DEFAULT 99,
  description TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// topology_relation_types 关系类型目录
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS topology_relation_types (
  name VARCHAR(64) PRIMARY KEY,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  display_name_en VARCHAR(128) NOT NULL DEFAULT '',
  builtin TINYINT NOT NULL DEFAULT 0,
  propagates_failure TINYINT NOT NULL DEFAULT 0,
  direction VARCHAR(16) NOT NULL DEFAULT 'src_to_dst',
  semantics_tag VARCHAR(32) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// platform_settings 平台设置（KV，从 CH 迁 MySQL；用 config_key 规避 key 保留字）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS platform_settings (
  config_key VARCHAR(128) PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// llm_providers LLM Provider（从 CH 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS llm_providers (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(64) DEFAULT 'openai_compatible',
  base_url VARCHAR(255) NOT NULL DEFAULT '',
  default_model VARCHAR(128) DEFAULT '',
  cost VARCHAR(64) DEFAULT '人民币',
  available TINYINT DEFAULT 1,
  enabled TINYINT DEFAULT 0,
  api_key_hash VARCHAR(64) DEFAULT '',
  api_key_encrypted TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// llm_config_history LLM 配置历史（从 CH 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS llm_config_history (
  version BIGINT PRIMARY KEY,
  provider VARCHAR(128) DEFAULT '',
  model VARCHAR(128) DEFAULT '',
  base_url VARCHAR(255) DEFAULT '',
  api_key_hash VARCHAR(64) DEFAULT '',
  operator VARCHAR(128) DEFAULT '',
  comment VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// alert_rules 告警规则（从 /tmp JSON 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS alert_rules (
  id VARCHAR(32) PRIMARY KEY,
  name VARCHAR(255) NOT NULL DEFAULT '',
  service VARCHAR(128) DEFAULT '',
  type VARCHAR(32) DEFAULT 'threshold',
  metric VARCHAR(64) DEFAULT '',
  cond VARCHAR(8) DEFAULT '>',
  threshold DOUBLE DEFAULT 0,
  duration INT DEFAULT 5,
  severity VARCHAR(16) DEFAULT 'warning',
  enabled TINYINT DEFAULT 1,
  webhook_url VARCHAR(512) DEFAULT '',
  cooldown INT DEFAULT 0,
  dampening INT DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// 兼容已存在的 alert_rules 表：补 webhook_url/cooldown/dampening 列（幂等）
	if !hasColumn(conn, "alert_rules", "webhook_url") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN webhook_url VARCHAR(512) DEFAULT ''")
	}
	if !hasColumn(conn, "alert_rules", "cooldown") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN cooldown INT DEFAULT 0")
	}
	if !hasColumn(conn, "alert_rules", "dampening") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN dampening INT DEFAULT 0")
	}
	// 批4: anomaly 基线窗口 / 检测方法 / SLO 引用（幂等）
	if !hasColumn(conn, "alert_rules", "baseline_seconds") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN baseline_seconds INT DEFAULT 900")
	}
	if !hasColumn(conn, "alert_rules", "anomaly_method") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN anomaly_method VARCHAR(16) DEFAULT 'zscore'")
	}
	if !hasColumn(conn, "alert_rules", "slo_id") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN slo_id VARCHAR(64) DEFAULT ''")
	}
	if !hasColumn(conn, "alert_rules", "keyword") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN keyword VARCHAR(255) DEFAULT ''")
	}

	// slo_targets SLO 目标（availability/latency，burn_rate 规则引用）
	conn.Exec(`CREATE TABLE IF NOT EXISTS slo_targets (
		id VARCHAR(64) PRIMARY KEY,
		name VARCHAR(128) NOT NULL,
		service VARCHAR(128) NOT NULL,
		slo_type VARCHAR(32) DEFAULT 'availability',
		target DECIMAL(10,4) NOT NULL DEFAULT 99.9,
		window_seconds INT DEFAULT 2592000,
		enabled TINYINT DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`)

	// dashboard_panels Monitor 看板面板（B4 完整看板）
	conn.Exec(`CREATE TABLE IF NOT EXISTS dashboard_panels (
		id VARCHAR(64) PRIMARY KEY,
		title VARCHAR(128) NOT NULL,
		query TEXT,
		chart_type VARCHAR(32) DEFAULT 'line',
		grid_x INT DEFAULT 0,
		grid_y INT DEFAULT 0,
		grid_w INT DEFAULT 6,
		grid_h INT DEFAULT 4,
		span INT DEFAULT 6,
		sort INT DEFAULT 0,
		enabled TINYINT DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`)
	// 种子面板（首次初始化 4 个默认面板）
	var panelCount int
	_ = conn.QueryRow("SELECT count(*) FROM dashboard_panels").Scan(&panelCount)
	if panelCount == 0 {
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
			_, _ = conn.Exec("INSERT IGNORE INTO dashboard_panels (id, title, query, chart_type, span, sort, enabled) VALUES (?, ?, ?, ?, ?, ?, 1)",
				sp.id, sp.title, sp.query, sp.chart, sp.span, i)
		}
	}

	// 告警事件已迁移到 ClickHouse（observability.alert_events，ReplacingMergeTree + TTL，见 init_clickhouse.sql），
	// MySQL 侧不再创建 alert_events 表（历史数据可清理）。

	// alert_silences 告警静默（从 /tmp JSON 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS alert_silences (
  id VARCHAR(32) PRIMARY KEY,
  service VARCHAR(128) DEFAULT '',
  rule_id VARCHAR(32) DEFAULT '',
  comment VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT NULL,
  expires_at DATETIME DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// tenants 租户（从 /tmp JSON 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS tenants (
  id VARCHAR(64) PRIMARY KEY,
  name VARCHAR(255) NOT NULL DEFAULT '',
  quota_ai INT DEFAULT 0,
  enabled TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
