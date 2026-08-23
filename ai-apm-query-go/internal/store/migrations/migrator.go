// Package migrations 实现 AIOps 统一 MySQL 版本化迁移器（V9.2 Phase 4）。
//
// 权威迁移元数据表 = aiops_schema_migrations（不复用/不 ALTER 旧 schema_migrations）。
// 结构：
//
//	migration_id VARCHAR(255) PRIMARY KEY   -- namespaced，如 mysql/0001-control-plane-baseline
//	checksum     CHAR(64) NOT NULL          -- 该迁移文件 SHA256(hex)
//	applied_at   DATETIME(3) NOT NULL
//
// Run 只能由独立的 cmd/schema-migrator（aiops_migrator 账号）执行，负责 DDL。
// query-api runtime 只调用 RequireCurrentVersion（只读，校验版本 + checksum），
// 禁止 CREATE/ALTER/INSERT。
//
// MySQL DDL 存在 implicit commit，无法用普通事务保证全文件原子回滚。因此：
//
//	GET_LOCK → 检查已应用 + 校验 checksum → 逐条幂等执行 → 全部成功后才 INSERT 记录 → RELEASE_LOCK
//
// 中途失败不标记 applied，靠 DDL 自身幂等（IF NOT EXISTS）继续恢复。语句用
// "-- statement-breakpoint" 分隔（不使用 Split(";")，避免 SQL 字符串/注释/存储过程里的分号破坏）。
package migrations

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
)

//go:embed versions/*.sql
var versionsFS embed.FS

var (
	// ErrSchemaOutdated 表示要求的 migration 尚未全部应用（runtime fail-closed）。
	ErrSchemaOutdated = errors.New("schema outdated: required migration missing")
	// ErrSchemaChecksumMismatch 表示已应用 migration 的内容（checksum）与 embed 不一致
	//（runtime fail-closed；可能是篡改或 schema 漂移）。
	ErrSchemaChecksumMismatch = errors.New("schema checksum mismatch: migration content drifted")
	// ErrMigrationLock 表示无法获得迁移锁。
	ErrMigrationLock = errors.New("could not acquire migration lock")
)

// Migration 描述一个待应用/已应用的版本化 schema 变更。
type Migration struct {
	ID         string   // namespaced id，如 mysql/0001-control-plane-baseline
	Checksum   string   // SHA256(hex) of the migration content
	Statements []string // 由 "-- statement-breakpoint" 分隔的语句
}

// Run 从 embed versions/*.sql 加载全部迁移并应用到 db。只能由 cmd/schema-migrator 调用。
func Run(db *sql.DB) error {
	ms, err := loadEmbedded()
	if err != nil {
		return err
	}
	return RunMigrations(db, ms)
}

// RunMigrations 将给定迁移应用到 db。核心实现（可注入，便于单测）。
func RunMigrations(db *sql.DB, ms []Migration) error {
	if err := ensureMetaTable(db); err != nil {
		return err
	}
	// 获取迁移锁，避免并发 migrator。
	var got int64
	if err := db.QueryRow("SELECT GET_LOCK(?, ?)", "aiops_migrate", int64(30)).Scan(&got); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer db.QueryRow("SELECT RELEASE_LOCK(?)", "aiops_migrate").Scan(new(int64))
	if got != 1 {
		return ErrMigrationLock
	}

	applied, err := loadApplied(db)
	if err != nil {
		return err
	}

	for _, m := range ms {
		existing, ok := applied[m.ID]
		if ok {
			if existing != m.Checksum {
				return fmt.Errorf("%w: migration_id=%s db_checksum=%s expected=%s",
					ErrSchemaChecksumMismatch, m.ID, existing, m.Checksum)
			}
			continue // 已应用且 checksum 一致 → skip
		}
		for _, stmt := range m.Statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				// 幂等恢复（R3 修正 5）：MySQL DDL 无事务原子性，若一个版本中途失败，
				// 版本不标记 applied，下次重跑时靠 DDL 自身幂等 + 这里捕获已存在类
				// 错误继续，而不是永久卡死。捕获：Duplicate column/key name、
				// table already exists（**不含 1062 Duplicate entry**，见 P0-4：数据级
				// 冲突是真实失败，不吞掉后标记迁移完成）。
				if isIdempotentDDLError(err) {
					continue
				}
				return fmt.Errorf("apply %s: %w", m.ID, err)
			}
		}
		if _, err := db.Exec(
			"INSERT INTO aiops_schema_migrations (migration_id, checksum) VALUES (?, ?)",
			m.ID, m.Checksum); err != nil {
			return fmt.Errorf("record %s: %w", m.ID, err)
		}
	}
	return nil
}

// RequireCurrent 只读 readiness check（runtime 用）：加载 embed versions 为 required
// 集合后校验版本 + checksum。只 SELECT，禁止 CREATE/ALTER/INSERT。
func RequireCurrent(db *sql.DB) error {
	ms, err := loadEmbedded()
	if err != nil {
		return err
	}
	return RequireCurrentVersion(db, ms)
}

// RequireCurrentVersion 只读 readiness check：校验所有 required migration 已应用且
// checksum 一致。缺失 → ErrSchemaOutdated；checksum 不一致 → ErrSchemaChecksumMismatch。
// 只 SELECT，禁止 CREATE/ALTER/INSERT。
func RequireCurrentVersion(db *sql.DB, ms []Migration) error {
	applied, err := loadApplied(db)
	if err != nil {
		return err
	}
	for _, m := range ms {
		existing, ok := applied[m.ID]
		if !ok {
			return fmt.Errorf("%w: missing %s", ErrSchemaOutdated, m.ID)
		}
		if existing != m.Checksum {
			return fmt.Errorf("%w: migration_id=%s db_checksum=%s expected=%s",
				ErrSchemaChecksumMismatch, m.ID, existing, m.Checksum)
		}
	}
	return nil
}

func ensureMetaTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS aiops_schema_migrations (
		migration_id VARCHAR(255) PRIMARY KEY,
		checksum CHAR(64) NOT NULL,
		applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
	)`)
	return err
}

func loadApplied(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query("SELECT migration_id, checksum FROM aiops_schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]string{}
	for rows.Next() {
		var id, cksum string
		if err := rows.Scan(&id, &cksum); err != nil {
			return nil, err
		}
		applied[id] = cksum
	}
	return applied, rows.Err()
}

// loadEmbedded 从 embed versions/*.sql 读取迁移，按文件名前缀版本号升序返回。
// Migration.ID = "mysql/<文件名不带扩展名>"（namespaced，避免与 orchestrator 的
// "0001" 碰撞）。Checksum = 文件内容 SHA256。
func loadEmbedded() ([]Migration, error) {
	entries, err := fs.ReadDir(versionsFS, "versions")
	if err != nil {
		return nil, err
	}
	// 按文件名前缀数字版本升序（0001 < 0002 < 0003）。
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var ms []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(versionsFS, "versions/"+e.Name())
		if err != nil {
			return nil, err
		}
		id := "mysql/" + strings.TrimSuffix(e.Name(), ".sql")
		ms = append(ms, Migration{
			ID:         id,
			Checksum:   sha256Hex(string(data)),
			Statements: splitStatements(string(data)),
		})
	}
	return ms, nil
}

// splitStatements 用行首的 "-- statement-breakpoint" 分隔迁移文件为多条语句。
// 只在行首匹配（前面是换行或文件开头），避免把出现在注释/字符串/存储过程体中的
// 同名字样误当作分隔标记（R3 修正 5：不使用 Split(";")）。
func splitStatements(content string) []string {
	const marker = "-- statement-breakpoint"
	var out []string
	var current strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), marker) {
			if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
				out = append(out, trimmed)
			}
			current.Reset()
			continue
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		out = append(out, trimmed)
	}
	return out
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// isIdempotentDDLError 判断是否为"对象已存在"类 DDL 错误，用于 migrator 的幂等恢复。
// MySQL 错误码：1060 Duplicate column name、1061 Duplicate key name、
// 1050 Table already exists、1062 Duplicate entry。也兼容文本匹配。
func isIdempotentDDLError(err error) bool {
	if err == nil {
		return false
	}
	if me, ok := err.(*mysql.MySQLError); ok {
		switch me.Number {
		case 1050, 1060, 1061:
			return true
		}
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"Duplicate column name",
		"Duplicate key name",
		"already exists",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	// 注意：不把 1062 / "Duplicate entry" 当幂等安全——它表示**数据级**冲突（如 UNIQUE 约束
	// 因重复数据添加失败，或数据 INSERT 撞唯一键），是真实失败，不能吞掉后标记迁移完成（P0-4）。
	return false
}
