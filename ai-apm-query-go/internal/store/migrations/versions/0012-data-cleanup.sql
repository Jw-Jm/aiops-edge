-- mysql/0012-data-cleanup
-- Durable preview, confirmation and operation state for administrator data cleanup.
CREATE TABLE IF NOT EXISTS data_cleanup_operations (
  operation_id CHAR(36) PRIMARY KEY,
  preview_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  user_id VARCHAR(128) NOT NULL DEFAULT '',
  idempotency_key VARCHAR(128) NOT NULL,
  request_digest CHAR(64) NOT NULL,
  confirmation_hash CHAR(64) NOT NULL,
  canonical_request JSON NOT NULL,
  plan_json JSON NOT NULL,
  result_json JSON NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'preview',
  expires_at DATETIME(3) NOT NULL,
  confirmed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_data_cleanup_preview (preview_id),
  UNIQUE KEY uq_data_cleanup_idempotency (tenant_id, idempotency_key),
  KEY idx_data_cleanup_status (status, updated_at),
  KEY idx_data_cleanup_tenant (tenant_id, created_at),
  CONSTRAINT chk_data_cleanup_status CHECK (status IN ('preview','queued','running','succeeded','failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
