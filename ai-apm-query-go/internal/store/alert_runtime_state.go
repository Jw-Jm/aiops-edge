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
	HolderID   string
	Epoch      int64
	TokenHash  string
	AcquiredAt *time.Time
	ExpiresAt  *time.Time
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
// P1（报告 §33）：全 DB-time——用 CURRENT_TIMESTAMP(3) 作为唯一时钟基准（跨 pod/跨机一致），
// 不再用应用侧 time.Now()（避免时钟偏移导致 Leader 竞争/误判过期）。
// 返回 (holderID, epoch, token, isLeader, error)。
func (d *AlertEvalLeaderDAO) Acquire(holderID string) (epoch int64, token string, isLeader bool, err error) {
	conn := GetDB()
	if conn == nil {
		return 0, "", false, errors.New("mysql unavailable")
	}
	raw, h := NewLeaseToken()
	tx, err := conn.Begin()
	if err != nil {
		return 0, "", false, err
	}
	defer tx.Rollback()

	var currentHolder string
	var currentEpoch int64
	err = tx.QueryRow(
		`SELECT holder_id, holder_epoch FROM aiops.alert_eval_leader WHERE leader_name='alert-eval'`,
	).Scan(&currentHolder, &currentEpoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}

	// 用 DB-time 原子判断"自己持有且未过期"或"可抢占"。
	// expires_at <= CURRENT_TIMESTAMP(3) 用 DB 时钟比较，避免应用侧时钟参与。
	if err == nil && currentHolder == holderID {
		// 自己持有 → 续约（epoch 不变，返回新 token）。CURRENT_TIMESTAMP(3)+INTERVAL 由 DB 计算。
		if _, err := tx.Exec(
			`UPDATE aiops.alert_eval_leader
			   SET token_hash=?, expires_at=CURRENT_TIMESTAMP(3) + INTERVAL ? SECOND,
			       acquired_at=CURRENT_TIMESTAMP(3)
			 WHERE leader_name='alert-eval' AND holder_id=? AND holder_epoch=?`,
			h, defaultAlertLeaseSeconds, holderID, currentEpoch,
		); err != nil {
			return 0, "", false, err
		}
		if err := tx.Commit(); err != nil {
			return 0, "", false, err
		}
		return currentEpoch, raw, true, nil
	}

	if err == nil {
		// 已存在记录：另一进程持有且未过期（DB-time 判断）→ held；否则抢占（epoch 递增）。
		var n int64
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM aiops.alert_eval_leader
			 WHERE leader_name='alert-eval' AND holder_id=? AND holder_id<>? AND expires_at > CURRENT_TIMESTAMP(3)`,
			currentHolder, holderID,
		).Scan(&n); err != nil {
			return 0, "", false, err
		}
		if n == 1 {
			return 0, "", false, ErrAlertLeaderHeld
		}
		if _, err := tx.Exec(
			`UPDATE aiops.alert_eval_leader
			   SET holder_id=?, holder_epoch=?, token_hash=?,
			       acquired_at=CURRENT_TIMESTAMP(3), expires_at=CURRENT_TIMESTAMP(3) + INTERVAL ? SECOND
			 WHERE leader_name='alert-eval' AND (holder_id='' OR expires_at IS NULL OR expires_at <= CURRENT_TIMESTAMP(3))`,
			holderID, currentEpoch+1, h, defaultAlertLeaseSeconds,
		); err != nil {
			return 0, "", false, err
		}
	} else {
		// 首次（无记录）→ 插入，时间全部 DB 生成。
		if _, err := tx.Exec(
			`INSERT INTO aiops.alert_eval_leader (leader_name, holder_id, holder_epoch, token_hash, acquired_at, expires_at)
			 VALUES ('alert-eval', ?, ?, ?, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3) + INTERVAL ? SECOND)`,
			holderID, 1, h, defaultAlertLeaseSeconds,
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
// P1（报告 §33）：expires_at 由 DB 时钟生成（CURRENT_TIMESTAMP(3) + INTERVAL），
// 避免应用侧时钟参与续约判断。
func (d *AlertEvalLeaderDAO) Renew(holderID string, epoch int64, token string) (time.Time, error) {
	conn := GetDB()
	if conn == nil {
		return time.Time{}, errors.New("mysql unavailable")
	}
	th := hashTokenForStore(token)
	var newExpires sql.NullTime
	res, err := conn.Exec(
		`UPDATE aiops.alert_eval_leader
		   SET expires_at=CURRENT_TIMESTAMP(3) + INTERVAL ? SECOND
		 WHERE leader_name='alert-eval' AND holder_id=? AND holder_epoch=? AND token_hash=?`,
		defaultAlertLeaseSeconds, holderID, epoch, th,
	)
	if err != nil {
		return time.Time{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return time.Time{}, ErrAlertLeaderFencing
	}
	// 读回续约后的 expires_at（DB-time），供调用方展示/判断。
	_ = conn.QueryRow(
		`SELECT expires_at FROM aiops.alert_eval_leader WHERE leader_name='alert-eval'`,
	).Scan(&newExpires)
	if newExpires.Valid {
		return newExpires.Time, nil
	}
	return time.Time{}, nil
}

func hashTokenForStore(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AlertRuleRuntimeState 是单条规则的 cooldown/dampening 运行时状态。
type AlertRuleRuntimeState struct {
	RuleID        string
	LastTriggerAt *time.Time
	BreachStreak  int
}

// AlertRuleRuntimeStateDAO 持久化 cooldown/dampening 状态（进程内 map 只能当缓存）。
type AlertRuleRuntimeStateDAO struct{}

// Upsert 写入规则运行时状态（幂等）。
// P1（报告 §33）：updated_at 用 DB 时钟（CURRENT_TIMESTAMP(3)），last_trigger_at 保留业务传入值。
func (d *AlertRuleRuntimeStateDAO) Upsert(s AlertRuleRuntimeState) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`INSERT INTO aiops.alert_rule_runtime_state (rule_id, last_trigger_at, breach_streak, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP(3))
		 ON DUPLICATE KEY UPDATE last_trigger_at=VALUES(last_trigger_at),
		   breach_streak=VALUES(breach_streak), updated_at=CURRENT_TIMESTAMP(3)`,
		s.RuleID, nullableTime(s.LastTriggerAt), s.BreachStreak,
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
