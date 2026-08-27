package store

import (
	"database/sql"
	"errors"
	"time"
)

type GraphProjectionOutbox struct {
	ID                 int64
	EventID            string
	TenantID           string
	ClusterID          string
	AggregateType      string
	AggregateID        string
	AggregateKeySHA256 string
	MutationKind       string
	EntityUID          string
	EdgeUID            string
	PayloadJSON        string
	AggregateVersion   int64
	Status             string
	RetryCount         int
	AvailableAt        time.Time
	LockedBy           string
	LockedUntil        *time.Time
	LastError          string
	CreatedAt          time.Time
	ProcessedAt        *time.Time
}

type GraphProjectionOutboxDAO struct{}

func (d *GraphProjectionOutboxDAO) List(limit int) ([]GraphProjectionOutbox, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT id, event_id, tenant_id, cluster_id, aggregate_type, aggregate_id,
    aggregate_key_sha256, mutation_kind, entity_uid, edge_uid, payload_json, aggregate_version,
    status, retry_count, available_at, locked_by, locked_until, last_error, created_at, processed_at
    FROM graph_projection_outbox ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGraphOutboxRows(rows)
}

func (d *GraphProjectionOutboxDAO) RetryByID(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_projection_outbox SET status='pending', available_at=NOW(), locked_by=NULL, locked_until=NULL,
    last_error=NULL WHERE id=? AND status IN ('failed','dead','processing')`, id)
	return err
}

func (d *GraphProjectionOutboxDAO) Insert(event GraphProjectionOutbox) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if event.Status == "" {
		event.Status = "pending"
	}
	if event.PayloadJSON == "" {
		event.PayloadJSON = "{}"
	}
	if event.AggregateKeySHA256 == "" {
		event.AggregateKeySHA256 = sha256Parts(event.AggregateType, event.AggregateID)
	}
	_, err := conn.Exec(`INSERT INTO graph_projection_outbox
    (event_id, tenant_id, cluster_id, aggregate_type, aggregate_id, aggregate_key_sha256,
     mutation_kind, entity_uid, edge_uid, payload_json, aggregate_version, status, retry_count)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE event_id = VALUES(event_id)`,
		event.EventID, event.TenantID, event.ClusterID, event.AggregateType, event.AggregateID,
		event.AggregateKeySHA256, event.MutationKind, event.EntityUID, event.EdgeUID,
		event.PayloadJSON, event.AggregateVersion, event.Status, event.RetryCount)
	return err
}

func (d *GraphProjectionOutboxDAO) ScanPending(limit int) ([]GraphProjectionOutbox, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := conn.Query(`SELECT id, event_id, tenant_id, cluster_id, aggregate_type, aggregate_id,
    aggregate_key_sha256, mutation_kind, entity_uid, edge_uid, payload_json, aggregate_version,
    status, retry_count, available_at, locked_by, locked_until, last_error, created_at, processed_at
    FROM graph_projection_outbox
    WHERE (status = 'pending' AND available_at <= NOW())
       OR (status = 'processing' AND locked_until IS NOT NULL AND locked_until <= NOW())
    ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGraphOutboxRows(rows)
}

func scanGraphOutboxRows(rows *sql.Rows) ([]GraphProjectionOutbox, error) {
	var out []GraphProjectionOutbox
	for rows.Next() {
		var item GraphProjectionOutbox
		var clusterID, entityUID, edgeUID, lockedBy, lastError sql.NullString
		var lockedUntil, processedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.EventID, &item.TenantID, &clusterID, &item.AggregateType,
			&item.AggregateID, &item.AggregateKeySHA256, &item.MutationKind, &entityUID, &edgeUID,
			&item.PayloadJSON, &item.AggregateVersion, &item.Status, &item.RetryCount, &item.AvailableAt,
			&lockedBy, &lockedUntil, &lastError, &item.CreatedAt, &processedAt); err != nil {
			return nil, err
		}
		item.ClusterID, item.EntityUID, item.EdgeUID, item.LockedBy, item.LastError = clusterID.String, entityUID.String, edgeUID.String, lockedBy.String, lastError.String
		if lockedUntil.Valid {
			item.LockedUntil = &lockedUntil.Time
		}
		if processedAt.Valid {
			item.ProcessedAt = &processedAt.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *GraphProjectionOutboxDAO) Claim(eventID, owner string, lease time.Duration) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	seconds := int64(lease.Seconds())
	if seconds <= 0 {
		seconds = 30
	}
	result, err := conn.Exec(`UPDATE graph_projection_outbox
    SET status = 'processing', locked_by = ?, locked_until = DATE_ADD(NOW(), INTERVAL ? SECOND), retry_count = retry_count + 1
    WHERE event_id = ? AND ((status = 'pending' AND available_at <= NOW())
      OR (status = 'processing' AND locked_until IS NOT NULL AND locked_until <= NOW()))`, owner, seconds, eventID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (d *GraphProjectionOutboxDAO) Done(eventID, owner string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_projection_outbox SET status='done', locked_by=NULL, locked_until=NULL, processed_at=NOW()
    WHERE event_id=? AND status='processing' AND locked_by=?`, eventID, owner)
	return err
}

func (d *GraphProjectionOutboxDAO) Retry(eventID, owner string, availableAt time.Time, lastError string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`UPDATE graph_projection_outbox SET status=IF(retry_count >= 10, 'dead', 'pending'),
    available_at=?, locked_by=NULL, locked_until=NULL, last_error=?
    WHERE event_id=? AND status='processing' AND locked_by=?`, availableAt, lastError, eventID, owner)
	return err
}
