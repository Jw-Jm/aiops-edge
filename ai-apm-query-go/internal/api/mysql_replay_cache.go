package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// A2-01（0004_runtime_convergence）：共享 TrustedRequest replay guard。
//
// 替代单进程 InMemoryReplayCache：query-api 多 Pod / 重启后，nonce 重放保护需跨进程一致。
// 用 MySQL ai_context_replay_guard 表（PK issuer+audience+nonce）做原子消费：
//   - 首次消费（nonce 不存在）→ INSERT，成功消费。
//   - 重复消费（nonce 已存在）→ 拒绝（context_replayed）。
//   - 过期 nonce 定期清理（expires_at < now）。
//
// 与 /internal/v1/security/replay/consume（A2-01）共用同一张表；但本 cache 由
// 验证器内部自动消费（验证 TrustedRequestContext 时即原子占用 nonce），
// /internal/v1/security/replay/consume 是给显式需要"先占后用"的调用方（如 RunInvocation
// 共享 context）用的。两者都只能由认证服务身份触发。
// ─────────────────────────────────────────────────────────────────────────────

// MySQLReplayCache implements trustedauth.ReplayCache backed by ai_context_replay_guard.
// Issuer/Audience 在构造时固定（即验证器的 issuer/audience），与 nonce 构成 PK。
type MySQLReplayCache struct {
	issuer   string
	audience string
}

// NewMySQLReplayCache 构造 MySQL 共享 replay cache。
func NewMySQLReplayCache(issuer, audience string) *MySQLReplayCache {
	return &MySQLReplayCache{issuer: issuer, audience: audience}
}

// CheckAndStore 原子消费 nonce：不存在 → 插入并消费成功；已存在 → 拒绝（重放）。
// now 为进程时钟；expiresAt 用于清理窗口。key = (issuer, audience, request_hash(nonce))。
func (c *MySQLReplayCache) CheckAndStore(nonce string, expiresAt, now time.Time) error {
	conn := store.GetDB()
	if conn == nil {
		return errors.New("mysql replay cache unavailable")
	}
	key := requestKey(nonce)
	// INSERT IGNORE：已存在（重放）→ RowsAffected=0，拒绝。跨 Pod 原子，唯一约束保证并发只一个成功。
	res, err := conn.Exec(
		`INSERT IGNORE INTO ai_context_replay_guard (issuer, audience, nonce, request_hash, consumed_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.issuer, c.audience, nonce, key, now, expiresAt,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errContextReplayed
	}
	// 异步清理过期（尽力而为，不阻塞验证）。
	go func() {
		conn.Exec(`DELETE FROM ai_context_replay_guard WHERE expires_at < CURRENT_TIMESTAMP(3)`)
	}()
	return nil
}

func requestKey(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

var errContextReplayed = errors.New("context_replayed")

// ConsumeReplayNonce 显式消费一个 nonce（供 /internal/v1/security/replay/consume 使用）。
// 返回 created=true 表示首次消费（成功占用）；false 表示已消费过（重放）。
func ConsumeReplayNonce(issuer, audience, nonce string, ttlSeconds int) (bool, error) {
	conn := store.GetDB()
	if conn == nil {
		return false, errors.New("mysql replay cache unavailable")
	}
	now := time.Now()
	expires := now.Add(time.Duration(ttlSeconds) * time.Second)
	res, err := conn.Exec(
		`INSERT IGNORE INTO ai_context_replay_guard (issuer, audience, nonce, request_hash, consumed_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		issuer, audience, nonce, requestKey(nonce), now, expires,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
