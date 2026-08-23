package migrations

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// TestAIRuntimeSchemaManifest 在可用 MySQL 上跑 schema-migrator 后，逐表核对
// V9.2 冻结 AI Runtime 表的列 / nullability / PK / unique（P1-1：字段来源
// docs/AIOPS_DATA_MODEL_REDESIGN.md）。无 MySQL 时跳过。
//
// 运行：MYSQL_HOST=... MYSQL_PORT=... MYSQL_USER=... MYSQL_PASSWORD=... MYSQL_DB=...
// go test ./internal/store/migrations/ -run TestAIRuntimeSchemaManifest -v
func TestAIRuntimeSchemaManifest(t *testing.T) {
	db := openTestMySQL(t)
	defer db.Close()

	if err := Run(db); err != nil {
		t.Fatalf("Run migrations: %v", err)
	}

	// ai_runs 冻结列存在；且不含 cluster_id 列（多集群由 ai_run_clusters 表达）。
	requiredCols := []string{"run_id", "request_id", "tenant_id", "principal", "scope_kind",
		"primary_cluster_id", "intent", "action_mode", "target_type", "target_resource_id",
		"status", "state_version", "parent_run_id"}
	for _, c := range requiredCols {
		if !columnExists(t, db, "ai_runs", c) {
			t.Errorf("ai_runs missing required column %s", c)
		}
	}
	if columnExists(t, db, "ai_runs", "cluster_id") {
		t.Errorf("ai_runs must NOT have cluster_id (multi-cluster expressed by ai_run_clusters)")
	}
	if !isNotNull(t, db, "ai_runs", "tenant_id") {
		t.Errorf("ai_runs.tenant_id must be NOT NULL")
	}

	// ai_run_clusters PK(run_id, cluster_id)
	if !columnExists(t, db, "ai_run_clusters", "cluster_id") {
		t.Errorf("ai_run_clusters missing cluster_id")
	}
	if !isNotNull(t, db, "ai_run_clusters", "cluster_id") {
		t.Errorf("ai_run_clusters.cluster_id must be NOT NULL")
	}
	if !primaryKeyIs(t, db, "ai_run_clusters", []string{"run_id", "cluster_id"}) {
		t.Errorf("ai_run_clusters PK must be (run_id, cluster_id)")
	}

	// 明细表 cluster_id NOT NULL：ai_tool_runs/ai_evidence/ai_hypotheses/ai_actions/ai_verifications/ai_approval_decisions
	for _, tbl := range []string{"ai_tool_runs", "ai_evidence", "ai_hypotheses", "ai_actions", "ai_verifications", "ai_approval_decisions"} {
		if !columnExists(t, db, tbl, "cluster_id") {
			t.Errorf("%s missing cluster_id", tbl)
		} else if !isNotNull(t, db, tbl, "cluster_id") {
			t.Errorf("%s.cluster_id must be NOT NULL", tbl)
		}
	}

	// aggregate ai_plan_steps.cluster_id 可 NULL
	if !columnExists(t, db, "ai_plan_steps", "cluster_id") {
		t.Errorf("ai_plan_steps missing cluster_id")
	} else if isNotNull(t, db, "ai_plan_steps", "cluster_id") {
		t.Errorf("ai_plan_steps.cluster_id must be NULL (aggregate)")
	}

	// ai_run_events PK(run_id, sequence)
	if !primaryKeyIs(t, db, "ai_run_events", []string{"run_id", "sequence"}) {
		t.Errorf("ai_run_events PK must be (run_id, sequence)")
	}
}

func openTestMySQL(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("MYSQL_HOST")
	if host == "" {
		t.Skip("MYSQL_HOST not set; skipping schema manifest test (needs real MySQL)")
	}
	port := os.Getenv("MYSQL_PORT")
	if port == "" {
		port = "3306"
	}
	user := os.Getenv("MYSQL_USER")
	if user == "" {
		user = "root"
	}
	pw := os.Getenv("MYSQL_PASSWORD")
	dbName := os.Getenv("MYSQL_DB")
	if dbName == "" {
		dbName = "aiops"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&tls=preferred", user, pw, host, port, dbName)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("mysql unreachable (%v); skipping", err)
	}
	return db
}

func columnExists(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, col).Scan(&n)
	if err != nil {
		t.Fatalf("columnExists %s.%s: %v", table, col, err)
	}
	return n > 0
}

func isNotNull(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	var nullable string
	err := db.QueryRow(`SELECT IS_NULLABLE FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, col).Scan(&nullable)
	if err != nil {
		t.Fatalf("isNotNull %s.%s: %v", table, col, err)
	}
	return strings.EqualFold(nullable, "NO")
}

func primaryKeyIs(t *testing.T, db *sql.DB, table string, want []string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY'
ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		t.Fatalf("primaryKeyIs %s: %v", table, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan pk %s: %v", table, err)
		}
		got = append(got, c)
	}
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
