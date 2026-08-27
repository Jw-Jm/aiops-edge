package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

type GraphWorkerLease struct {
	LeaseKey   string
	OwnerID    string
	LeaseEpoch int64
	TokenHash  string
	ExpiresAt  time.Time
}

type GraphWorkerLeaseDAO struct{}

func NewGraphLeaseToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func (d *GraphWorkerLeaseDAO) Acquire(leaseKey, ownerID string, ttl time.Duration) (GraphWorkerLease, bool, error) {
	conn := GetDB()
	if conn == nil {
		return GraphWorkerLease{}, false, errors.New("mysql unavailable")
	}
	seconds := int64(ttl.Seconds())
	if seconds <= 0 {
		seconds = 30
	}
	token := NewGraphLeaseToken()
	_, err := conn.Exec(`INSERT INTO graph_worker_leases (lease_key, owner_id, lease_epoch, token_hash, expires_at)
    VALUES (?, ?, 1, ?, DATE_ADD(NOW(), INTERVAL ? SECOND))
    ON DUPLICATE KEY UPDATE owner_id=IF(expires_at <= NOW(), VALUES(owner_id), owner_id),
      lease_epoch=IF(expires_at <= NOW(), lease_epoch+1, lease_epoch),
      token_hash=IF(expires_at <= NOW(), VALUES(token_hash), token_hash),
      expires_at=IF(expires_at <= NOW(), VALUES(expires_at), expires_at)`, leaseKey, ownerID, token, seconds)
	if err != nil {
		return GraphWorkerLease{}, false, err
	}
	var lease GraphWorkerLease
	err = conn.QueryRow(`SELECT lease_key, owner_id, lease_epoch, token_hash, expires_at
    FROM graph_worker_leases
    WHERE lease_key=? AND owner_id=? AND token_hash=? AND expires_at > NOW()`, leaseKey, ownerID, token).
		Scan(&lease.LeaseKey, &lease.OwnerID, &lease.LeaseEpoch, &lease.TokenHash, &lease.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GraphWorkerLease{}, false, nil
	}
	if err != nil {
		return GraphWorkerLease{}, false, err
	}
	return lease, true, nil
}

func (d *GraphWorkerLeaseDAO) Renew(lease GraphWorkerLease, ttl time.Duration) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	seconds := int64(ttl.Seconds())
	if seconds <= 0 {
		seconds = 30
	}
	result, err := conn.Exec(`UPDATE graph_worker_leases SET expires_at=DATE_ADD(NOW(), INTERVAL ? SECOND)
    WHERE lease_key=? AND owner_id=? AND token_hash=? AND expires_at > NOW()`, seconds, lease.LeaseKey, lease.OwnerID, lease.TokenHash)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

// Verify is the fencing check used by source reconcile control-plane phases.
// The caller must present the exact owner, epoch and token returned by
// Acquire; an expired or replaced lease can never mutate graph state.
func (d *GraphWorkerLeaseDAO) Verify(leaseKey, ownerID, token string, epoch int64) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	var found int
	err := conn.QueryRow(`SELECT COUNT(*) FROM graph_worker_leases
    WHERE lease_key=? AND owner_id=? AND lease_epoch=? AND token_hash=? AND expires_at > NOW()`,
		leaseKey, ownerID, epoch, token).Scan(&found)
	return found == 1, err
}

// Release expires a lease without deleting its fencing epoch/history.
func (d *GraphWorkerLeaseDAO) Release(leaseKey, ownerID, token string, epoch int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_worker_leases SET expires_at=NOW()
    WHERE lease_key=? AND owner_id=? AND lease_epoch=? AND token_hash=?`, leaseKey, ownerID, epoch, token)
	return err
}
