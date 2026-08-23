package migrations

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestMigratedSchemaCoversLegacyEnsureSchema 证明 authoritative migrations 覆盖
// legacy EnsureSchema 的 Phase 4 runtime-required 对象（含 LEGACY 但当前 runtime
// 仍依赖的表）。无 MySQL 时跳过；真实 Gate（P4.9 环境 A）用它验证。
//
// 方法：
//   - 两个隔离 schema：fresh（跑 schema-migrator → snapshot A）、legacy（跑
//     legacy store.EnsureSchema → snapshot B）。
//   - 对所有 Phase 4 runtime-required 表：required tables/columns/indexes A covers B。
func TestMigratedSchemaCoversLegacyEnsureSchema(t *testing.T) {
	if os.Getenv("MYSQL_HOST") == "" {
		t.Skip("MYSQL_HOST not set; skipping coverage test (needs real MySQL)")
	}
	admin := openRootMySQL(t)
	defer admin.Close()

	// 用隔离库，避免污染当前环境（R3 P0-F / 修正 8：不 DROP 当前验收库）。
	dbNameA := "aiops_p4_gate_fresh"
	dbNameB := "aiops_p4_gate_legacy"
	mustExec(t, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbNameA))
	mustExec(t, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbNameB))
	mustExec(t, admin, fmt.Sprintf("CREATE DATABASE %s", dbNameA))
	mustExec(t, admin, fmt.Sprintf("CREATE DATABASE %s", dbNameB))
	defer mustExec(t, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbNameA))
	defer mustExec(t, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbNameB))

	// Snapshot A: authoritative migrations.
	a := openDBAs(t, dbNameA)
	if err := Run(a); err != nil {
		t.Fatalf("fresh schema-migrator: %v", err)
	}

	// Snapshot B: legacy EnsureSchema on controlled reference DB.
	b := openDBAs(t, dbNameB)
	prev := store.GetDB()
	store.SetDB(b)
	store.EnsureSchema()
	store.SetDB(prev)

	// required tables: snapshot A must contain every table snapshot B creates,
	// and every required column/index of that table.
	required := []string{
		"users", "service_catalog", "devices", "clusters", "topology_nodes",
		"topology_relations", "topology_node_types", "topology_relation_types",
		"platform_settings", "llm_providers", "llm_config_history", "alert_rules",
		"slo_targets", "dashboard_panels", "alert_silences", "tenants",
		"service_metadata", "anomaly_events", "audit_logs",
		"auth_sessions", "user_tenants", "roles", "permissions", "user_roles",
		"role_permissions", "scope_assignments", "tenant_clusters",
		"ai_runs", "ai_run_clusters", "ai_plan_steps", "ai_tool_runs", "ai_evidence",
		"ai_hypotheses", "ai_actions", "ai_verifications", "ai_approval_decisions",
		"ai_run_events", "ai_audit_events", "platform_audit_events",
		"approval_tasks", "reports", "change_events",
	}
	for _, tbl := range required {
		if !tableExistsIn(t, a, tbl) {
			t.Errorf("migrated schema A is missing required table %s (legacy B creates it)", tbl)
			continue
		}
		// every column B has for this table must exist in A
		for col := range tableColumnsIn(t, b, tbl) {
			if !columnExistsIn(t, a, tbl, col) {
				t.Errorf("migrated schema A missing column %s.%s", tbl, col)
			}
		}
	}

	// 幂等：再跑一次 migrator，版本数不变。
	before := migrationCount(t, a)
	if err := Run(a); err != nil {
		t.Fatalf("second migrator run: %v", err)
	}
	after := migrationCount(t, a)
	if before != after {
		t.Fatalf("migrator not idempotent: versions %d → %d", before, after)
	}
}

func openRootMySQL(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("MYSQL_HOST")
	user := os.Getenv("MYSQL_USER")
	if user == "" {
		user = "root"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/?parseTime=true&tls=preferred", user, os.Getenv("MYSQL_PASSWORD"), host, os.Getenv("MYSQL_PORT"))
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open admin mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("mysql unreachable (%v); skipping", err)
	}
	return db
}

func openDBAs(t *testing.T, name string) *sql.DB {
	t.Helper()
	host := os.Getenv("MYSQL_HOST")
	user := os.Getenv("MYSQL_USER")
	if user == "" {
		user = "root"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&tls=preferred", user, os.Getenv("MYSQL_PASSWORD"), host, os.Getenv("MYSQL_PORT"), name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db %s: %v", name, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("mysql unreachable (%v); skipping", err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func tableExistsIn(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&n)
	if err != nil {
		t.Fatalf("tableExists %s: %v", table, err)
	}
	return n > 0
}

func tableColumnsIn(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table)
	if err != nil {
		t.Fatalf("columns %s: %v", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan col %s: %v", table, err)
		}
		out[c] = true
	}
	return out
}

func columnExistsIn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	return tableColumnsIn(t, db, table)[col]
}

func migrationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM aiops_schema_migrations").Scan(&n); err != nil {
		t.Fatalf("migration count: %v", err)
	}
	return n
}
