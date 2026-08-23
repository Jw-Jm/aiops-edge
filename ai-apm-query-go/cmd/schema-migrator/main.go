// Command schema-migrator 是 V9.2 Phase 4 的 MySQL schema 迁移执行器。
//
// 它只运行在初始化 Job 中，使用 aiops_migrator 账号（唯一允许 DDL 的 MySQL 账号），
// 按版本顺序应用 internal/store/migrations/versions/*.sql，写入权威迁移元数据表
// aiops_schema_migrations。query-api runtime 不得调用本命令的迁移逻辑，只做只读
// RequireCurrentVersion readiness check。
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

func main() {
	db, err := openDB()
	if err != nil {
		log.Fatalf("schema-migrator: open db: %v", err)
	}
	defer db.Close()

	if err := migrations.Run(db); err != nil {
		log.Fatalf("schema-migrator: %v", err)
	}
	fmt.Println("schema-migrator: migrations applied successfully")
}

// openDB 用 MYSQL_* 环境变量（期望 MYSQL_USER=aiops_migrator）打开 MySQL 连接。
// DSN 走 tcp，charset=utf8mb4，多语句关闭（语句由 migrator 按 -- statement-breakpoint 拆分）。
func openDB() (*sql.DB, error) {
	host := env("MYSQL_HOST", "127.0.0.1")
	port := env("MYSQL_PORT", "3306")
	user := env("MYSQL_USER", "aiops_migrator")
	password := os.Getenv("MYSQL_PASSWORD")
	name := env("MYSQL_DB", "aiops")
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true",
		user, password, host, port, name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
