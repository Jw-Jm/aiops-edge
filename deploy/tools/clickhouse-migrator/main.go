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
//   - 0 行          → 未应用 → 执行 SQL → 成功后记录真实 SHA256
//   - 1 行、checksum 一致 → SKIP
//   - 1 行、checksum 不一致 → FAIL CLOSED（先比较 checksum，绝不先执行 SQL）
//   - >1 行         → metadata 损坏 → FAIL CLOSED
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

const identityDefaultMigrationID = "0009_k8s_events_require_identity"
const topologySummingMigrationID = "0010_service_topology_summing"

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
			// 某些兼容迁移的目标状态可能已由初始化脚本或人工修复达到。
			// 先确认目标状态，再决定是否执行 SQL；无论哪条路径，都只有成功后才记录。
			alreadySatisfied, err := migrationTargetAlreadySatisfied(endpoint, user, pass, id)
			if err != nil {
				fatalf("inspect target state %s: %v (not recorded)", id, err)
			}
			if !alreadySatisfied {
				// 未应用：执行 SQL（先拆语句，逐条 HTTP 执行）。
				if err := executeSQL(endpoint, user, pass, id, string(data)); err != nil {
					fatalf("apply %s: %v (not recorded)", id, err)
				}
			}
			if err := recordApplied(endpoint, user, pass, id, checksum); err != nil {
				fatalf("record %s: %v", id, err)
			}
			if alreadySatisfied {
				fmt.Printf("APPLIED   %s (target state already satisfied)\n", id)
			} else {
				fmt.Printf("APPLIED   %s\n", id)
			}
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

// migrationTargetAlreadySatisfied handles migrations whose SQL is not accepted
// when the desired state is already present.  Fresh ClickHouse schemas omit the
// event_id DEFAULT, while legacy schemas need 0009 to remove it.  Asking
// system.columns first makes the migration idempotent across both states and
// still fails closed if the expected column is missing or ambiguous.
func migrationTargetAlreadySatisfied(endpoint, user, pass, id string) (bool, error) {
	if id == topologySummingMigrationID {
		q := "SELECT engine FROM system.tables WHERE database = 'observability' AND name = 'service_topology' FORMAT TabSeparated"
		resp, err := clickhouseQuery(endpoint, user, pass, q)
		if err != nil {
			return false, err
		}
		return topologyEngineTargetSatisfied(resp), nil
	}
	if id != identityDefaultMigrationID {
		return false, nil
	}
	q := "SELECT if(default_kind = '', '__none__', default_kind) FROM system.columns " +
		"WHERE database = 'observability' AND table = 'k8s_events' AND name = 'event_id' FORMAT TabSeparated"
	resp, err := clickhouseQuery(endpoint, user, pass, q)
	if err != nil {
		return false, err
	}
	var rows []string
	for _, row := range strings.Split(strings.TrimSpace(resp), "\n") {
		if value := strings.TrimSpace(row); value != "" {
			rows = append(rows, value)
		}
	}
	if len(rows) == 0 {
		return false, fmt.Errorf("observability.k8s_events.event_id not found")
	}
	if len(rows) != 1 {
		return false, fmt.Errorf("observability.k8s_events.event_id returned %d metadata rows", len(rows))
	}
	return identityDefaultTargetSatisfied(rows[0]), nil
}

func topologyEngineTargetSatisfied(engine string) bool {
	return strings.TrimSpace(engine) == "SummingMergeTree"
}

func identityDefaultTargetSatisfied(defaultKind string) bool {
	return strings.TrimSpace(defaultKind) == "__none__"
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

// splitStatements 按分号拆分，正确处理 SQL 字符串、标识符和行/块注释。
// ClickHouse 迁移包含正则表达式与字符串字面量，不能用“是否包含 ')”之类
// 的启发式判断语句边界，否则迁移会把后续 DDL 拼接进同一条请求。
func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	var inSingle, inDouble, inBacktick, inLineComment, inBlockComment bool
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		var next byte
		if i+1 < len(sql) {
			next = sql[i+1]
		}
		if inLineComment {
			if c == '\n' {
				inLineComment = false
				cur.WriteByte(c)
			}
			continue
		}
		if inBlockComment {
			if c == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if !inSingle && !inDouble && !inBacktick {
			switch {
			case c == '-' && next == '-':
				inLineComment = true
				i++
				continue
			case c == '/' && next == '*':
				inBlockComment = true
				i++
				continue
			case c == ';':
				flush()
				continue
			case c == '\'':
				inSingle = true
			case c == '"':
				inDouble = true
			case c == '`':
				inBacktick = true
			}
			cur.WriteByte(c)
			continue
		}

		cur.WriteByte(c)
		if c == '\\' && (inSingle || inDouble) && i+1 < len(sql) {
			cur.WriteByte(sql[i+1])
			i++
			continue
		}
		switch {
		case inSingle && c == '\'':
			if next == '\'' { // SQL 中的转义单引号 ''
				cur.WriteByte(next)
				i++
			} else {
				inSingle = false
			}
		case inDouble && c == '"':
			inDouble = false
		case inBacktick && c == '`':
			inBacktick = false
		}
	}
	flush()
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
