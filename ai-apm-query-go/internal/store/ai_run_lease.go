package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// A1（0004_runtime_convergence）：Run execution Lease + epoch/token fencing。
//
// 并发契约（报告 §18.2 / 27.2）：
//   - 两个 executor 竞争一个 Run，只能一个成为 owner（claim 原子抢占）。
//   - lease_epoch 单调递增：old epoch 的 commit/action 必须被 fenced（拒绝）。
//   - lease_token_hash 是 owner 持有的随机 token 的 SHA256（只存 hash，不存明文）。
//   - lease_expires_at 用 DB time（CURRENT_TIMESTAMP(3)）判定过期，不用进程内时钟，
//     避免多副本时钟偏差导致 Lease 判断错误。
//
// 与 ai_run_claims 的关系：每次 claim 写一条 claim 历史（run_id, claim_id, executor,
// epoch, token_hash, created_at），用于审计"谁在哪个 epoch 持锁"。
// ─────────────────────────────────────────────────────────────────────────────

// RunLeaseHolder 描述一次 Run 执行 Lease 的当前持有状态。
type RunLeaseHolder struct {
	RunID        string
	OwnerID      string
	Epoch        int64
	ClaimID      string
	Token        string // 明文 token（仅 claim 成功时返回给 owner，DB 不存）
	TokenHash    string
	ExpiresAt    time.Time
	WaitKind     string
	RetryBefore  *time.Time
	RetryAttempt int
}

// RuntimeLeaseDAO 访问 ai_runs lease 列 + ai_run_claims。
type RuntimeLeaseDAO struct{}

// NewLeaseToken 生成随机的 lease token 并返回 (明文, SHA256 hash)。
// 明文只返回给 claim 成功的 owner；DB 只存 hash。
func NewLeaseToken() (string, string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:])
}

// Claim 原子抢占 Run 的执行 Lease。
// 规则：
//   - Run 已终态 → ErrRunTerminal（不可再 claim）。
//   - 当前有活跃 Lease（owner 相同且未过期）→ 返回既有权（幂等）。
//   - 当前 Lease 被其它 owner 持有且未过期 → 失败（ErrLeaseHeld），不抢占。
//   - 当前 Lease 过期/为空 → 原子抢占，epoch 递增，写 claim 历史。
//   - retry 等待中（runtime_wait_kind=retry 且 retry_not_before 在未来）→ 失败（ErrRetryBackoff）。
// 所有时间判定用 DB time（CURRENT_TIMESTAMP(3)），避免进程时钟偏差。
func (d *RuntimeLeaseDAO) Claim(runID, ownerID string, leaseSeconds int) (RunLeaseHolder, error) {
	conn := GetDB()
	if conn == nil {
		return RunLeaseHolder{}, errors.New("mysql unavailable")
	}
	now := time.Now()
	// 1) 读当前状态 + 终态检查（同事务）。
	tx, err := conn.Begin()
	if err != nil {
		return RunLeaseHolder{}, err
	}
	defer tx.Rollback()

	var status string
	var curOwner sql.NullString
	var curEpoch, retryAttempt int64
	var curExpires sql.NullTime
	var waitKind sql.NullString
	var retryBefore sql.NullTime
	err = tx.QueryRow(
		`SELECT status, lease_owner_id, lease_epoch, lease_expires_at, runtime_wait_kind, retry_attempt, retry_not_before
		 FROM ai_runs WHERE run_id = ? FOR UPDATE`, runID,
	).Scan(&status, &curOwner, &curEpoch, &curExpires, &waitKind, &retryAttempt, &retryBefore)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunLeaseHolder{}, ErrRunNotFound
		}
		return RunLeaseHolder{}, err
	}
	if isTerminalStatus(status) {
		return RunLeaseHolder{}, ErrRunTerminal
	}
	// retry backoff：runtime_wait_kind=retry 且 retry_not_before 在未来 → 不可 claim。
	if waitKind.Valid && waitKind.String == "retry" && retryBefore.Valid && retryBefore.Time.After(now) {
		return RunLeaseHolder{}, &RetryBackoffError{NotBefore: retryBefore.Time}
	}
	// 已有活跃 Lease。
	if curOwner.Valid && curOwner.String != "" && curExpires.Valid && curExpires.Time.After(now) {
		if curOwner.String == ownerID {
			// 同 owner 续约语义：返回既有权（不递增 epoch，保持 token；明文不重发，token hash 保留）。
			return RunLeaseHolder{
				RunID: runID, OwnerID: ownerID, Epoch: curEpoch, ExpiresAt: curExpires.Time,
				WaitKind: waitKind.String, RetryAttempt: int(retryAttempt), RetryBefore: nil,
			}, nil
		}
		return RunLeaseHolder{}, ErrLeaseHeld
	}

	// 2) 抢占：epoch 递增，新 token，新 owner，更新 expires_at。
	rawToken, tokenHash := NewLeaseToken()
	claimID := NewUUIDv4()
	newEpoch := curEpoch + 1
	newExpires := now.Add(time.Duration(leaseSeconds) * time.Second)
	if _, err := tx.Exec(
		`UPDATE ai_runs SET lease_owner_id = ?, lease_epoch = ?, lease_claim_id = ?,
		   lease_token_hash = ?, lease_expires_at = ?, heartbeat_at = ?,
		   runtime_wait_kind = 'none', retry_not_before = NULL
		 WHERE run_id = ? AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		ownerID, newEpoch, claimID, tokenHash, newExpires, now, runID,
	); err != nil {
		return RunLeaseHolder{}, err
	}
	// 3) 写 claim 历史（审计）。
	if _, err := tx.Exec(
		`INSERT INTO ai_run_claims (run_id, claim_id, executor_id, lease_epoch, lease_token_hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		runID, claimID, ownerID, newEpoch, tokenHash, now,
	); err != nil {
		return RunLeaseHolder{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunLeaseHolder{}, err
	}
	return RunLeaseHolder{
		RunID: runID, OwnerID: ownerID, Epoch: newEpoch, ClaimID: claimID,
		Token: rawToken, TokenHash: tokenHash, ExpiresAt: newExpires, WaitKind: "none",
	}, nil
}

// Renew 续约 Lease。要求 owner + epoch + token 匹配（fencing），否则拒绝。
// 用于 owner 在长任务执行期间持续心跳续约。
func (d *RuntimeLeaseDAO) Renew(runID, ownerID string, epoch int64, tokenHash string, leaseSeconds int) (time.Time, error) {
	conn := GetDB()
	if conn == nil {
		return time.Time{}, errors.New("mysql unavailable")
	}
	now := time.Now()
	newExpires := now.Add(time.Duration(leaseSeconds) * time.Second)
	res, err := conn.Exec(
		`UPDATE ai_runs SET lease_expires_at = ?, heartbeat_at = ?
		 WHERE run_id = ? AND lease_owner_id = ? AND lease_epoch = ? AND lease_token_hash = ?
		   AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		newExpires, now, runID, ownerID, epoch, tokenHash,
	)
	if err != nil {
		return time.Time{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return time.Time{}, ErrLeaseFencing
	}
	return newExpires, nil
}

// Release 主动释放 Lease（owner 完成/失败后调用）。要求 epoch + token 匹配（防 old owner 释放 new owner）。
func (d *RuntimeLeaseDAO) Release(runID string, epoch int64, tokenHash string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		`UPDATE ai_runs SET lease_owner_id = NULL, lease_epoch = lease_epoch, lease_claim_id = NULL,
		   lease_token_hash = NULL, lease_expires_at = NULL, heartbeat_at = NULL
		 WHERE run_id = ? AND lease_epoch = ? AND lease_token_hash = ?`,
		runID, epoch, tokenHash,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeaseFencing
	}
	return nil
}

// ScanRecoveryCandidates 扫描需要恢复的非终态 Run：
//   - 无活跃 Lease（lease_owner_id 为空 或 lease_expires_at 已过 DB-time）。
//   - 不处于 retry backoff（runtime_wait_kind != 'retry' 或 retry_not_before <= now）。
//
// 有活跃 Lease 的 Run 由当前 owner 继续，不列为恢复候选（避免双 executor 抢同一个活跃 Run）。
// 分页 limit>0。
func (d *RuntimeLeaseDAO) ScanRecoveryCandidates(limit int) ([]RunLeaseHolder, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	q := `SELECT run_id, lease_owner_id, lease_epoch, lease_claim_id, lease_token_hash,
		   lease_expires_at, runtime_wait_kind, retry_not_before, retry_attempt
		 FROM ai_runs
		 WHERE status NOT IN ('success','partial','failed','regressed','cancelled')
		   AND (lease_owner_id IS NULL OR lease_expires_at IS NULL OR lease_expires_at < CURRENT_TIMESTAMP(3))
		   AND (runtime_wait_kind <> 'retry' OR retry_not_before IS NULL OR retry_not_before <= CURRENT_TIMESTAMP(3))`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = conn.Query(q+` ORDER BY created_at ASC LIMIT ?`, limit)
	} else {
		rows, err = conn.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunLeaseHolder{}
	for rows.Next() {
		var h RunLeaseHolder
		var owner, claimID, tokenHash, waitKind sql.NullString
		var epoch, retryAttempt sql.NullInt64
		var expires, retryBefore sql.NullTime
		if err := rows.Scan(&h.RunID, &owner, &epoch, &claimID, &tokenHash, &expires,
			&waitKind, &retryBefore, &retryAttempt); err != nil {
			return nil, err
		}
		h.OwnerID = owner.String
		h.Epoch = epoch.Int64
		h.ClaimID = claimID.String
		h.TokenHash = tokenHash.String
		if expires.Valid {
			h.ExpiresAt = expires.Time
		}
		h.WaitKind = waitKind.String
		if retryBefore.Valid {
			h.RetryBefore = &retryBefore.Time
		}
		h.RetryAttempt = int(retryAttempt.Int64)
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetRuntimeMetadataTx 在给定事务内读取 Run 的 runtime/lease 元数据（恢复一致性快照用）。
func (d *RuntimeLeaseDAO) GetRuntimeMetadataTx(tx *sql.Tx, runID string) (*RunLeaseHolder, error) {
	var h RunLeaseHolder
	var owner, waitKind, tokenHash, claimID sql.NullString
	var epoch, retryAttempt sql.NullInt64
	var expires, heartbeat, retryBefore sql.NullTime
	err := tx.QueryRow(
		`SELECT lease_owner_id, lease_epoch, lease_claim_id, lease_token_hash, lease_expires_at,
		   heartbeat_at, runtime_wait_kind, retry_not_before, retry_attempt
		 FROM ai_runs WHERE run_id = ?`, runID,
	).Scan(&owner, &epoch, &claimID, &tokenHash, &expires, &heartbeat, &waitKind, &retryBefore, &retryAttempt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	h.RunID = runID
	h.OwnerID = owner.String
	h.Epoch = epoch.Int64
	h.ClaimID = claimID.String
	h.TokenHash = tokenHash.String
	if expires.Valid {
		h.ExpiresAt = expires.Time
	}
	h.WaitKind = waitKind.String
	if retryBefore.Valid {
		h.RetryBefore = &retryBefore.Time
	}
	h.RetryAttempt = int(retryAttempt.Int64)
	return &h, nil
}

// LeaseExpired 用 DB time 判断 Run 的 Lease 是否已过期（用于 recovery 决策）。
func (d *RuntimeLeaseDAO) LeaseExpired(runID string) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	var n int64
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM ai_runs WHERE run_id = ? AND lease_expires_at IS NOT NULL
		   AND lease_expires_at < CURRENT_TIMESTAMP(3)`, runID,
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ─── errors ──────────────────────────────────────────────────────────────
var (
	ErrLeaseHeld    = errors.New("run lease held by another owner")
	ErrLeaseFencing = errors.New("run lease epoch/token fencing mismatch")
	ErrRunTerminal  = errors.New("run is in terminal state")
	ErrRunNotFound  = errors.New("run not found")
)

// RetryBackoffError 表示 Run 处于 retry backoff（retry_not_before 前不可 claim）。
type RetryBackoffError struct{ NotBefore time.Time }

func (e *RetryBackoffError) Error() string {
	return "run in retry backoff until " + e.NotBefore.Format(time.RFC3339)
}

// LeaseFencingTx 在给定事务内校验 Lease fencing（owner + epoch + token 匹配，且未过期）。
// 供 Runtime Commit 在最终权威更新前重新校验 DB-time Lease（报告 28.2："Commit 在最终权威更新
// 前重新校验 DB-time Lease"）。返回 valid 布尔。
func LeaseFencingTx(tx *sql.Tx, runID, ownerID string, epoch int64, tokenHash string) (bool, error) {
	var n int64
	err := tx.QueryRow(
		`SELECT COUNT(*) FROM ai_runs WHERE run_id = ? AND lease_owner_id = ? AND lease_epoch = ?
		   AND lease_token_hash = ? AND lease_expires_at IS NOT NULL
		   AND lease_expires_at >= CURRENT_TIMESTAMP(3)
		   AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		runID, ownerID, epoch, tokenHash,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// NewUUIDv4 生成 RFC4122 v4 UUID 字符串（claim_id 等用）。
func NewUUIDv4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
