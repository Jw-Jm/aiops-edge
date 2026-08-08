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
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
