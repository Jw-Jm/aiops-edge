package store

import (
	"database/sql"
	"errors"
	"time"
)

// AIActionOutbox is the durable command envelope for an approved Action. The
// action hash/version are copied into the envelope so a dispatcher can fence
// stale rows before crossing the executor mutation boundary.
type AIActionOutbox struct {
	CommandID       string
	ActionID        string
	ActionVersion   int64
	ActionHash      string
	RunID           string
	TenantID        string
	ClusterID       string
	Status          string
	DispatchCount   int64
	NextRetryAt     *time.Time
	DispatchOwnerID string
	DispatchEpoch   int64
	DispatchToken   string
	DispatchExpiry  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AIActionOutboxDAO owns claim/deliver/retry fencing for Action commands.
type AIActionOutboxDAO struct{}

func (d *AIActionOutboxDAO) Insert(o AIActionOutbox) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	status := o.Status
	if status == "" {
		status = "pending"
	}
	_, err := conn.Exec(`INSERT INTO ai_action_outbox
		(command_id, action_id, action_version, action_hash, run_id, tenant_id, cluster_id,
		 status, dispatch_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE command_id = command_id`,
		o.CommandID, o.ActionID, o.ActionVersion, o.ActionHash, o.RunID, o.TenantID, o.ClusterID,
		status, o.DispatchCount, firstTime(o.CreatedAt, time.Now()), firstTime(o.UpdatedAt, time.Now()))
	return err
}

func (d *AIActionOutboxDAO) Claim(commandID, ownerID string, lease time.Duration) (DispatchFence, bool, error) {
	conn := GetDB()
	if conn == nil {
		return DispatchFence{}, false, errors.New("mysql unavailable")
	}
	fence := NewDispatchFence(ownerID)
	leaseSec := int64(lease.Seconds())
	if leaseSec <= 0 {
		leaseSec = 30
	}
	res, err := conn.Exec(`UPDATE ai_action_outbox
		SET status = 'claimed', dispatch_count = dispatch_count + 1,
			dispatch_owner_id = ?, dispatch_epoch = ?, dispatch_token_hash = ?,
			dispatch_expires_at = DATE_ADD(NOW(), INTERVAL ? SECOND),
			next_retry_at = DATE_ADD(NOW(), INTERVAL ? SECOND), updated_at = NOW()
		WHERE command_id = ?
		  AND (status = 'pending' OR (status = 'claimed' AND dispatch_expires_at IS NOT NULL AND dispatch_expires_at <= NOW()))`,
		fence.OwnerID, fence.Epoch, fence.TokenHash, leaseSec, leaseSec, commandID)
	if err != nil {
		return DispatchFence{}, false, err
	}
	n, _ := res.RowsAffected()
	return fence, n == 1, nil
}

func (d *AIActionOutboxDAO) Deliver(commandID string, fence DispatchFence) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE ai_action_outbox SET status = 'delivered', delivered_at = NOW(), updated_at = NOW()
		WHERE command_id = ? AND dispatch_owner_id = ? AND dispatch_epoch = ? AND dispatch_token_hash = ?`,
		commandID, fence.OwnerID, fence.Epoch, fence.TokenHash)
	return err
}

func (d *AIActionOutboxDAO) Retry(commandID string, fence DispatchFence, nextRetryAt time.Time) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE ai_action_outbox SET status = 'pending', next_retry_at = ?, updated_at = NOW()
		WHERE command_id = ? AND dispatch_owner_id = ? AND dispatch_epoch = ? AND dispatch_token_hash = ?`,
		nextRetryAt, commandID, fence.OwnerID, fence.Epoch, fence.TokenHash)
	return err
}

func (d *AIActionOutboxDAO) ScanPending(limit int) ([]AIActionOutbox, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := conn.Query(`SELECT command_id, action_id, action_version, action_hash, run_id, tenant_id, cluster_id,
		status, dispatch_count, next_retry_at, dispatch_owner_id, dispatch_epoch, dispatch_token_hash,
		dispatch_expires_at, created_at, updated_at
		FROM ai_action_outbox
		WHERE (status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW()))
		   OR (status = 'claimed' AND dispatch_expires_at IS NOT NULL AND dispatch_expires_at <= NOW())
		ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIActionOutbox
	for rows.Next() {
		var o AIActionOutbox
		var retry, expiry sql.NullTime
		var ownerText, tokenText sql.NullString
		if err := rows.Scan(&o.CommandID, &o.ActionID, &o.ActionVersion, &o.ActionHash, &o.RunID,
			&o.TenantID, &o.ClusterID, &o.Status, &o.DispatchCount, &retry, &ownerText,
			&o.DispatchEpoch, &tokenText, &expiry, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		if retry.Valid {
			o.NextRetryAt = &retry.Time
		}
		if ownerText.Valid {
			o.DispatchOwnerID = ownerText.String
		}
		if tokenText.Valid {
			o.DispatchToken = tokenText.String
		}
		if expiry.Valid {
			o.DispatchExpiry = &expiry.Time
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
