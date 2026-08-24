package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// C-03（0005_alert_worker_state.sql）：Alert 单 Leader + cooldown/dampening MySQL 持久化。
//
// 1) AlertEvalLeaderDAO：alert-eval 角色单 Leader 租约（K8s Lease 语义的 MySQL 实现）。
//    - Acquire：原子抢占（无 holder 或过期可回收），epoch 递增 + 随机 token（只存 hash）。
//    - Renew：holder + epoch + token 匹配且未过期 → 续约；否则 fencing 拒绝。
//    - IsLeader：判断当前进程是否持有有效 Leader 租约。
//    多 alert-eval pod 只有一个能评估（避免重复告警事件）。
//
// 2) AlertRuleRuntimeStateDAO：cooldown/dampening 运行时状态持久化。
//    - Upsert：记录 last_trigger_at / breach_streak（跨 pod / 重启不丢失冷却期与 streak）。
//    - Get：读回状态（进程内 map 只能当缓存）。
// ─────────────────────────────────────────────────────────────────────────────

// AlertEvalLeader 表示一次 Leader 租约持有状态。
type AlertEvalLeader struct {
	HolderID    string
	Epoch       int64
	TokenHash   string
	AcquiredAt  *time.Time
	ExpiresAt   *time.Time
}

var (
	// ErrAlertLeaderHeld 另一进程持有 Leader 且未过期。
	ErrAlertLeaderHeld = errors.New("alert leader held")
	// ErrAlertLeaderFencing 续约 fencing 失败（epoch/token 不匹配或已过期）。
	ErrAlertLeaderFencing = errors.New("alert leader fencing")
)

// AlertEvalLeaderDAO 管理 alert-eval 单 Leader 租约。
type AlertEvalLeaderDAO struct{}

const defaultAlertLeaseSeconds = 120

// Acquire 尝试获取/保持 alert-eval Leader 租约。
// 返回 (holderID, epoch, token, isLeader, error)。
func (d *AlertEvalLeaderDAO) Acquire(holderID string) (epoch int64, token string, isLeader bool, err error) {
	conn := GetDB()
	if conn == nil {
		return 0, "", false, errors.New("mysql unavailable")
	}
	raw, h := NewLeaseToken()
	now := time.Now()
	expires := now.Add(defaultAlertLeaseSeconds * time.Second)
	tx, err := conn.Begin()
	if err != nil {
		return 0, "", false, err
	}
	defer tx.Rollback()

	var currentHolder string
	var currentEpoch int64
	var expiresAt sql.NullTime
	err = tx.QueryRow(
		`SELECT holder_id, holder_epoch, expires_at FROM aiops.alert_eval_leader WHERE leader_name='alert-eval'`,
	).Scan(&currentHolder, &currentEpoch, &expiresAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}

	if err == nil {
		// 已存在记录：另一进程持有且未过期 → held；否则可回收（无 holder / 已过期）。
		if currentHolder != "" && currentHolder != holderID &&
			expiresAt.Valid && expiresAt.Time.After(now) {
			return 0, "", false, ErrAlertLeaderHeld
		}
		if currentHolder == holderID {
			// 自己持有 → 续约（epoch 不变，返回新 token）
			if _, err := tx.Exec(
				`UPDATE aiops.alert_eval_leader SET token_hash=?, expires_at=?, acquired_at=? WHERE leader_name='alert-eval'`,
				h, expires, now,
			); err != nil {
				return 0, "", false, err
			}
			if err := tx.Commit(); err != nil {
				return 0, "", false, err
			}
			return currentEpoch, raw, true, nil
		}
		// 过期/无 holder → 抢占（epoch 递增）
		if _, err := tx.Exec(
			`UPDATE aiops.alert_eval_leader SET holder_id=?, holder_epoch=?, token_hash=?, acquired_at=?, expires_at=?
			 WHERE leader_name='alert-eval' AND (holder_id='' OR expires_at IS NULL OR expires_at <= ?)`,
			holderID, currentEpoch+1, h, now, expires, now,
		); err != nil {
			return 0, "", false, err
		}
	} else {
		// 首次（无记录）→ 插入
		if _, err := tx.Exec(
			`INSERT INTO aiops.alert_eval_leader (leader_name, holder_id, holder_epoch, token_hash, acquired_at, expires_at)
			 VALUES ('alert-eval', ?, ?, ?, ?, ?)`,
			holderID, 1, h, now, expires,
		); err != nil {
			return 0, "", false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, "", false, err
	}
	return currentEpoch + 1, raw, true, nil
}

// IsLeader 判断当前 holder 是否仍持有有效 Leader 租约（fencing：epoch+token）。
func (d *AlertEvalLeaderDAO) IsLeader(holderID string, epoch int64, token string) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	th := hashTokenForStore(token)
	var n int64
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM aiops.alert_eval_leader
		 WHERE leader_name='alert-eval' AND holder_id=? AND holder_epoch=? AND token_hash=?
		   AND expires_at IS NOT NULL AND expires_at >= CURRENT_TIMESTAMP(3)`,
		holderID, epoch, th,
	).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

// Renew 续约 Leader 租约（holder+epoch+token fencing）。
func (d *AlertEvalLeaderDAO) Renew(holderID string, epoch int64, token string) (time.Time, error) {
	conn := GetDB()
	if conn == nil {
		return time.Time{}, errors.New("mysql unavailable")
	}
	th := hashTokenForStore(token)
	expires := time.Now().Add(defaultAlertLeaseSeconds * time.Second)
	res, err := conn.Exec(
		`UPDATE aiops.alert_eval_leader SET expires_at=?
		 WHERE leader_name='alert-eval' AND holder_id=? AND holder_epoch=? AND token_hash=?`,
		expires, holderID, epoch, th,
	)
	if err != nil {
		return time.Time{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return time.Time{}, ErrAlertLeaderFencing
	}
	return expires, nil
}

func hashTokenForStore(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AlertRuleRuntimeState 是单条规则的 cooldown/dampening 运行时状态。
type AlertRuleRuntimeState struct {
	RuleID         string
	LastTriggerAt  *time.Time
	BreachStreak   int
}

// AlertRuleRuntimeStateDAO 持久化 cooldown/dampening 状态（进程内 map 只能当缓存）。
type AlertRuleRuntimeStateDAO struct{}

// Upsert 写入规则运行时状态（幂等）。
func (d *AlertRuleRuntimeStateDAO) Upsert(s AlertRuleRuntimeState) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	now := time.Now()
	_, err := conn.Exec(
		`INSERT INTO aiops.alert_rule_runtime_state (rule_id, last_trigger_at, breach_streak, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE last_trigger_at=VALUES(last_trigger_at),
		   breach_streak=VALUES(breach_streak), updated_at=VALUES(updated_at)`,
		s.RuleID, nullableTime(s.LastTriggerAt), s.BreachStreak, now,
	)
	return err
}

// Get 读取规则运行时状态。
func (d *AlertRuleRuntimeStateDAO) Get(ruleID string) (*AlertRuleRuntimeState, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var s AlertRuleRuntimeState
	var lastTrigger sql.NullTime
	if err := conn.QueryRow(
		`SELECT rule_id, last_trigger_at, breach_streak FROM aiops.alert_rule_runtime_state WHERE rule_id=?`,
		ruleID,
	).Scan(&s.RuleID, &lastTrigger, &s.BreachStreak); err != nil {
		return nil, err
	}
	if lastTrigger.Valid {
		t := lastTrigger.Time
		s.LastTriggerAt = &t
	}
	return &s, nil
}
