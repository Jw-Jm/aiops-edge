package store

import (
	"errors"
	"time"
)

// ─── AIRunOutbox ──────────────────────────────────────────────
// ai_run_outbox：Run 创建后的可靠派发（durable outbox / pull-claim）。
// query-api 持久化 Run 后写 outbox（pending）；dispatcher 扫描 pending → claim →
// 派发可信 RunInvocation 给 orchestrator → deliver。orchestrator 长时间不可用时
// 保留 pending，指数退避重试（dispatch_count / next_retry_at），Run 状态不推进。

// AIRunOutbox DB 实体。
type AIRunOutbox struct {
	InvocationID string
	RunID        string
	Status       string // pending|claimed|delivered|expired
	DispatchCount int64
	NextRetryAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AIRunOutboxDAO 访问 ai_run_outbox 表。
type AIRunOutboxDAO struct{}

// Insert 幂等写入派发记录（同 invocation_id 重复返回 nil，不报错）。
func (d *AIRunOutboxDAO) Insert(o AIRunOutbox) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	status := o.Status
	if status == "" {
		status = "pending"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_run_outbox (invocation_id, run_id, status, dispatch_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE invocation_id = invocation_id`,
		o.InvocationID, o.RunID, status, o.DispatchCount, o.CreatedAt, o.UpdatedAt)
	return err
}

// Claim 抢占一条 pending 派发记录（原子，RowsAffected==1 表示抢到）。
// 只抢占 pending，避免重复 claim 在途（claimed）记录；in-flight 由 lease 保护，
// 崩溃后 lease 过期由 ScanPending 回收（见 ScanPending 的 stale claimed 分支）。
func (d *AIRunOutboxDAO) Claim(invocationID string, lease time.Duration) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		`UPDATE ai_run_outbox SET status = 'claimed', dispatch_count = dispatch_count + 1,
		   next_retry_at = ?, updated_at = ?
		 WHERE invocation_id = ? AND status = 'pending'`,
		time.Now().Add(lease), time.Now(), invocationID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Deliver 标记派发成功。
func (d *AIRunOutboxDAO) Deliver(invocationID string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_run_outbox SET status = 'delivered', updated_at = ? WHERE invocation_id = ?`,
		time.Now(), invocationID)
	return err
}

// Retry 派发失败后设置下次重试时间（指数退避），保持 pending。
func (d *AIRunOutboxDAO) Retry(invocationID string, nextRetryAt time.Time) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_run_outbox SET status = 'pending', next_retry_at = ?, updated_at = ?
		 WHERE invocation_id = ?`,
		nextRetryAt, time.Now(), invocationID)
	return err
}

// ScanPending 扫描可派发记录：pending（next_retry_at 空或已到）+ 崩溃的 stale claimed
// （lease 过期，之前 dispatcher 崩溃留下的 in-flight 项，回收重派发）。
func (d *AIRunOutboxDAO) ScanPending(limit int) ([]AIRunOutbox, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 50
	}
	now := time.Now()
	rows, err := conn.Query(
		`SELECT invocation_id, run_id, status, dispatch_count, next_retry_at, created_at, updated_at
		 FROM ai_run_outbox
		 WHERE (status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= ?))
		    OR (status = 'claimed' AND next_retry_at IS NOT NULL AND next_retry_at <= ?)
		 ORDER BY created_at ASC LIMIT ?`,
		now, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIRunOutbox{}
	for rows.Next() {
		var o AIRunOutbox
		var retry *time.Time
		if err := rows.Scan(&o.InvocationID, &o.RunID, &o.Status, &o.DispatchCount, &retry,
			&o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.NextRetryAt = retry
		out = append(out, o)
	}
	return out, rows.Err()
}
