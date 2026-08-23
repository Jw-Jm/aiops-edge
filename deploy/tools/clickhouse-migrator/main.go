// Command clickhouse-migrator 是 V9.2 Phase 4 (P4.5) 的 ClickHouse 版本化迁移执行器。
//
// 它只运行在部署侧 bootstrap/migration Job 中，使用 dedicated migration 账号，
// 按版本顺序应用 deploy/helm/aiops/files/clickhouse/migrations/*.sql，并把已应用
// 记录写入 observability.aiops_schema_migrations。
//
// 行为（P0-C 状态机）：
//   - 每个迁移文件 = 一个 migration。migration_id = 文件名（不带 .sql）。
//   - checksum = 迁移文件【原始字节】的 SHA256(hex, lowercase)。
//   - 查询 observability.aiops_schema_migrations：
//       * 0 行          → 未应用 → 执行 SQL → 成功后记录真实 SHA256
//       * 1 行、checksum 一致 → SKIP
//       * 1 行、checksum 不一致 → FAIL CLOSED（先比较 checksum，绝不先执行 SQL）
//       * >1 行         → metadata 损坏 → FAIL CLOSED
//
// 运行时服务（event-collector / query-api / orchestrator）不得执行迁移，只能做
// 只读 schema compatibility check。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const metaTable = "observability.aiops_schema_migrations"

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
)

func main() {
	var dir, endpoint, user, pass string
	flag.StringVar(&dir, "dir", envOr("CLICKHOUSE_MIGRATIONS_DIR", "migrations"), "directory of ordered migration SQL files")
	flag.StringVar(&endpoint, "endpoint", envOr("CLICKHOUSE_URL", "http://127.0.0.1:8123"), "ClickHouse HTTP endpoint")
	flag.StringVar(&user, "user", envOr("CLICKHOUSE_USER", ""), "ClickHouse migration user")
	flag.StringVar(&pass, "password", envOr("CLICKHOUSE_PASSWORD", ""), "ClickHouse migration password")
	flag.Parse()

	files, err := orderedFiles(dir)
	if err != nil {
		fatalf("list migrations: %v", err)
	}

	// 确保 metadata 表存在（DDL，migrator 专用账号）。
	if err := ensureMetaTable(endpoint, user, pass); err != nil {
		fatalf("ensure metadata table: %v", err)
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			fatalf("read %s: %v", f, err)
		}
		id := strings.TrimSuffix(filepath.Base(f), ".sql")
		checksum := sha256HexBytes(data)

		applied, err := lookupApplied(endpoint, user, pass, id)
		if err != nil {
			fatalf("lookup %s: %v", id, err)
		}
		switch {
		case applied == nil:
			// 未应用：执行 SQL（先拆语句，逐条 HTTP 执行），成功后才记录。
			if err := executeSQL(endpoint, user, pass, id, string(data)); err != nil {
				fatalf("apply %s: %v (not recorded)", id, err)
			}
			if err := recordApplied(endpoint, user, pass, id, checksum); err != nil {
				fatalf("record %s: %v", id, err)
			}
			fmt.Printf("APPLIED   %s\n", id)
		case applied.Checksum == checksum:
			fmt.Printf("SKIPPED   %s (already applied, checksum matches)\n", id)
		default:
			// checksum mismatch → 先 fail closed，不执行任何 SQL。
			fatalf("CHECKSUM_MISMATCH migration=%s stored=%s actual=%s (abort before any SQL)",
				id, applied.Checksum, checksum)
		}
	}
	fmt.Println("clickhouse-migrator: all migrations applied/skipped successfully")
}

type appliedMeta struct {
	Checksum string
}

// orderedFiles 返回目录下 *.sql 按文件名字典序（版本前缀升序）的路径。
func orderedFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func ensureMetaTable(endpoint, user, pass string) error {
	// 先确保 observability 库存在。
	if err := execQuery(endpoint, user, pass, "CREATE DATABASE IF NOT EXISTS observability"); err != nil {
		return err
	}
	// 与迁移 SQL 中一致：migration_id String + checksum String + applied_at。
	// 注意：ClickHouse MergeTree 的 ORDER BY 不是 UNIQUE，runner 自行保证幂等与
	// 损坏检测（>1 行 → fail closed）。
	ddl := `CREATE TABLE IF NOT EXISTS ` + metaTable + ` (
  migration_id String,
  checksum String,
  applied_at DateTime DEFAULT now(),
  PRIMARY KEY (migration_id)
) ENGINE = MergeTree
ORDER BY migration_id`
	return execQuery(endpoint, user, pass, ddl)
}

// lookupApplied 查询某 migration 的已应用 checksum。
// 返回 nil 表示未应用；0/1/>1 行按 P0-C 语义处理（>1 行 = 损坏 → fail closed）。
func lookupApplied(endpoint, user, pass, id string) (*appliedMeta, error) {
	q := fmt.Sprintf("SELECT checksum FROM %s WHERE migration_id = '%s'", metaTable, id)
	resp, err := clickhouseQuery(endpoint, user, pass, q)
	if err != nil {
		return nil, err
	}
	rows := strings.Split(strings.TrimSpace(resp), "\n")
	// 过滤空行
	var nonEmpty []string
	for _, r := range rows {
		if strings.TrimSpace(r) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(r))
		}
	}
	switch len(nonEmpty) {
	case 0:
		return nil, nil
	case 1:
		return &appliedMeta{Checksum: nonEmpty[0]}, nil
	default:
		return nil, fmt.Errorf("metadata corruption: migration_id=%s has %d rows", id, len(nonEmpty))
	}
}

func recordApplied(endpoint, user, pass, id, checksum string) error {
	q := fmt.Sprintf("INSERT INTO %s (migration_id, checksum) VALUES ('%s', '%s')", metaTable, id, checksum)
	return execQuery(endpoint, user, pass, q)
}

// executeSQL 拆分迁移文件为单条语句，逐条 HTTP 执行。拆分用分号（ClickHouse
// DDL 单条无内部语句级分号；注释用 -- 处理到行尾）。
func executeSQL(endpoint, user, pass, id, sql string) error {
	for _, stmt := range splitStatements(sql) {
		if err := execQuery(endpoint, user, pass, stmt); err != nil {
			return fmt.Errorf("statement in %s: %w", id, err)
		}
	}
	return nil
}

// splitStatements 按分号拆分（跳过 -- 注释行与字符串里的分号）。
func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	lines := strings.Split(sql, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue // 注释行
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if strings.Contains(line, ";") && !strings.Contains(line, "')") {
			// 语句结束（行内含分号且非字符串内——ClickHouse DDL 简单场景足够）
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func clickhouseQuery(endpoint, user, pass, query string) (string, error) {
	// 读查询（SELECT）用 GET（ClickHouse HTTP readonly）；返回结果文本。
	return doRequest(endpoint, user, pass, query, http.MethodGet)
}

// execQuery 执行写查询（CREATE/INSERT 等）。ClickHouse HTTP 的 GET 只读，写必须用 POST。
func execQuery(endpoint, user, pass, query string) error {
	_, err := doRequest(endpoint, user, pass, query, http.MethodPost)
	return err
}

func doRequest(endpoint, user, pass, query, method string) (string, error) {
	u := endpoint + "/?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return "", err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("query error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "clickhouse-migrator: "+format+"\n", args...)
	os.Exit(1)
}
