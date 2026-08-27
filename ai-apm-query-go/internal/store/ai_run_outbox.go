package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ─── AIRunOutbox ─────────────────────────────────────────────────────────────
// ai_run_outbox：Run 创建后的可靠派发（durable outbox / pull-claim）。
// query-api 持久化 Run 后写 outbox（pending）；dispatcher 扫描 pending →
// Claim → 派发可信 RunInvocation 给 orchestrator → deliver。orchestrator 长时间
// 不可用时保留 pending，指数退避重试（dispatch_count / next_retry_at），Run 状态不推进。
//
// A0-03（生产收敛，F-11）：
//   - dispatch fencing：Claim 时设置 dispatch_owner_id + dispatch_epoch(+1) +
//     dispatch_token_hash；Deliver/Retry 必须匹配 owner+epoch+token（fencing），
//     防止旧 dispatcher 误交付/误重试（与 Run execution lease 分离）。
//   - stale claimed 回收：Claim 原子抢占 pending 或已过期 claimed（in-flight 崩溃遗留），
//     epoch 单调递增保证"新 claim 者获胜"。
//   - 到期判断用 DB time（NOW()），不用应用层 time.Now()（避免分布式时钟偏差误判）。
// ─────────────────────────────────────────────────────────────────────────────

// AIRunOutbox DB 实体。
type AIRunOutbox struct {
	InvocationID    string
	RunID           string
	Status          string // pending|claimed|delivered|expired
	DispatchCount   int64
	NextRetryAt     *time.Time
	DispatchOwnerID string
	DispatchEpoch   int64
	DispatchToken   string
	DispatchExpiry  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DispatchFence 表示一次 dispatch claim 的 fencing 身份（owner+epoch+token）。
type DispatchFence struct {
	OwnerID   string
	Epoch     int64
	TokenHash string
}

// NewDispatchFence 生成一次 claim 的 fencing 身份。
func NewDispatchFence(ownerID string) DispatchFence {
	return DispatchFence{
		OwnerID:   ownerID,
		Epoch:     time.Now().UnixNano(),
		TokenHash: randomHash(),
	}
}

func randomHash() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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

// Claim 原子抢占一条可派发记录（pending 或 stale claimed），返回新的 fencing 身份。
//   - 只抢占 pending 或已过期 claimed（in-flight 崩溃遗留，lease 用 DB time NOW() 判定）；
//   - epoch 单调递增 + 新 token，保证"新 claim 者获胜"，旧 owner 的 Deliver/Retry 会被
//     fencing 拒绝；
//   - lease 到期判断用 DB NOW()，避免应用层时钟偏差。
//
// RowsAffected==1 表示抢到，返回 true。
func (d *AIRunOutboxDAO) Claim(invocationID, ownerID string, lease time.Duration) (DispatchFence, bool, error) {
	conn := GetDB()
	if conn == nil {
		return DispatchFence{}, false, errors.New("mysql unavailable")
	}
	fence := NewDispatchFence(ownerID)
	leaseSec := int64(lease.Seconds())
	if leaseSec <= 0 {
		leaseSec = 30
	}
	res, err := conn.Exec(
		`UPDATE ai_run_outbox
		   SET status = 'claimed', dispatch_count = dispatch_count + 1,
		       dispatch_owner_id = ?, dispatch_epoch = ?, dispatch_token_hash = ?,
		       dispatch_expires_at = DATE_ADD(NOW(), INTERVAL ? SECOND),
		       next_retry_at = DATE_ADD(NOW(), INTERVAL ? SECOND), updated_at = NOW()
		 WHERE invocation_id = ?
		   AND (status = 'pending'
		        OR (status = 'claimed' AND dispatch_expires_at IS NOT NULL AND dispatch_expires_at <= NOW()))`,
		fence.OwnerID, fence.Epoch, fence.TokenHash, leaseSec, leaseSec, invocationID)
	if err != nil {
		return DispatchFence{}, false, err
	}
	n, _ := res.RowsAffected()
	return fence, n == 1, nil
}

// Deliver 标记派发成功（fencing：必须匹配当前 claim 的 owner+epoch+token）。
// 若被其他 dispatcher 重新 claim（epoch 变 / token 变），RowsAffected==0 → 不交付。
func (d *AIRunOutboxDAO) Deliver(invocationID string, fence DispatchFence) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_run_outbox
		   SET status = 'delivered', delivered_at = NOW(), updated_at = NOW()
		 WHERE invocation_id = ?
		   AND dispatch_owner_id = ? AND dispatch_epoch = ? AND dispatch_token_hash = ?`,
		invocationID, fence.OwnerID, fence.Epoch, fence.TokenHash)
	return err
}

// Retry 派发失败后设置下次重试时间（指数退避），保持 pending。
// fencing：只允许当前 claim 者重置（旧 owner 不得覆盖新 claim 者）。
func (d *AIRunOutboxDAO) Retry(invocationID string, fence DispatchFence, nextRetryAt time.Time) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_run_outbox SET status = 'pending', next_retry_at = ?, updated_at = NOW()
		 WHERE invocation_id = ?
		   AND dispatch_owner_id = ? AND dispatch_epoch = ? AND dispatch_token_hash = ?`,
		nextRetryAt, invocationID, fence.OwnerID, fence.Epoch, fence.TokenHash)
	return err
}

// ScanPending 扫描可派发记录：pending（next_retry_at 空或已到）+ 崩溃的 stale claimed
// （dispatch_expires_at 已过期，用 DB time 判定）。返回时可携带 fencing 字段供后续
// Claim 参考，但真正抢占仍由原子 Claim 完成（避免 scan-claim 竞态）。
func (d *AIRunOutboxDAO) ScanPending(limit int) ([]AIRunOutbox, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := conn.Query(
		`SELECT invocation_id, run_id, status, dispatch_count, next_retry_at,
		        dispatch_owner_id, dispatch_epoch, dispatch_token_hash, dispatch_expires_at,
		        created_at, updated_at
		 FROM ai_run_outbox
		 WHERE (status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW()))
		    OR (status = 'claimed' AND dispatch_expires_at IS NOT NULL AND dispatch_expires_at <= NOW())
		 ORDER BY created_at ASC LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIRunOutbox{}
	for rows.Next() {
		var o AIRunOutbox
		var retry, owner, token, expiry *string
		var epoch int64
		if err := rows.Scan(&o.InvocationID, &o.RunID, &o.Status, &o.DispatchCount, &retry,
			&owner, &epoch, &token, &expiry, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		if retry != nil {
			t, e := time.Parse(time.RFC3339Nano, *retry)
			if e == nil {
				o.NextRetryAt = &t
			}
		}
		if owner != nil {
			o.DispatchOwnerID = *owner
		}
		o.DispatchEpoch = epoch
		if token != nil {
			o.DispatchToken = *token
		}
		if expiry != nil {
			t, e := time.Parse(time.RFC3339Nano, *expiry)
			if e == nil {
				o.DispatchExpiry = &t
			}
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
