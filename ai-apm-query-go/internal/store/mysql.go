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
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

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
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// topology_nodes 拓扑顶点（typed property graph，对齐 ongrid）
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
  condition VARCHAR(8) DEFAULT '>',
  threshold DOUBLE DEFAULT 0,
  duration INT DEFAULT 5,
  severity VARCHAR(16) DEFAULT 'warning',
  enabled TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// alert_events 告警事件（从 /tmp JSON 迁 MySQL）
	_, _ = conn.Exec(`
CREATE TABLE IF NOT EXISTS alert_events (
  id VARCHAR(32) PRIMARY KEY,
  rule_id VARCHAR(32) DEFAULT '',
  rule_name VARCHAR(255) DEFAULT '',
  service VARCHAR(128) DEFAULT '',
  severity VARCHAR(16) DEFAULT 'warning',
  message TEXT,
  value DOUBLE DEFAULT 0,
  threshold DOUBLE DEFAULT 0,
  timestamp DATETIME DEFAULT NULL,
  count INT DEFAULT 1,
  first_timestamp DATETIME DEFAULT NULL,
  last_timestamp DATETIME DEFAULT NULL,
  status VARCHAR(20) DEFAULT 'firing',
  acknowledged_at DATETIME DEFAULT NULL,
  acknowledged_by VARCHAR(64) DEFAULT '',
  resolved_at DATETIME DEFAULT NULL,
  resolved_by VARCHAR(64) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

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
