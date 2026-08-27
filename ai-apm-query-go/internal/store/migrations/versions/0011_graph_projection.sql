-- mysql/0011-graph-projection
-- MySQL remains the authority; these tables hold graph projection metadata,
-- source watermarks, durable mutations, and historical RCA graph context.
CREATE TABLE IF NOT EXISTS graph_projection_outbox (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  event_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NULL,
  aggregate_type VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(512) NOT NULL,
  aggregate_key_sha256 CHAR(64) NOT NULL,
  mutation_kind VARCHAR(32) NOT NULL,
  entity_uid VARCHAR(512) NULL,
  edge_uid VARCHAR(96) NULL,
  payload_json JSON NOT NULL,
  aggregate_version BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  retry_count INT NOT NULL DEFAULT 0,
  available_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  locked_by VARCHAR(128) NULL,
  locked_until DATETIME(3) NULL,
  last_error VARCHAR(2048) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  processed_at DATETIME(3) NULL,
  UNIQUE KEY uq_graph_outbox_event (event_id),
  UNIQUE KEY uq_graph_outbox_version (aggregate_type, aggregate_key_sha256, aggregate_version),
  KEY idx_graph_outbox_pending (status, available_at, id),
  KEY idx_graph_outbox_lock (status, locked_until),
  KEY idx_graph_outbox_scope (tenant_id, cluster_id, id),
  CONSTRAINT chk_graph_outbox_kind CHECK (mutation_kind IN ('upsert_vertex','delete_vertex','upsert_edge','delete_edge')),
  CONSTRAINT chk_graph_outbox_status CHECK (status IN ('pending','processing','done','dead'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_sync_state (
  source VARCHAR(64) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  scope_cluster_id CHAR(36) NOT NULL,
  generation BIGINT NOT NULL DEFAULT 0,
  watermark VARCHAR(512) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'idle',
  last_started_at DATETIME(3) NULL,
  last_success_at DATETIME(3) NULL,
  last_error VARCHAR(2048) NOT NULL DEFAULT '',
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (source, tenant_id, scope_cluster_id),
  CONSTRAINT chk_graph_sync_status CHECK (status IN ('idle','running','success','failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_worker_leases (
  lease_key VARCHAR(255) PRIMARY KEY,
  owner_id VARCHAR(128) NOT NULL,
  lease_epoch BIGINT NOT NULL DEFAULT 0,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_graph_worker_lease_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_entity_alias (
  alias_id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  scope_cluster_id CHAR(36) NOT NULL,
  source VARCHAR(64) NOT NULL,
  alias_type VARCHAR(32) NOT NULL,
  alias_value VARCHAR(512) NOT NULL,
  alias_value_sha256 CHAR(64) NOT NULL,
  canonical_entity_uid VARCHAR(512) NOT NULL,
  confidence DOUBLE NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  resolver VARCHAR(64) NOT NULL DEFAULT 'deterministic',
  first_seen_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_seen_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_graph_alias (tenant_id, scope_cluster_id, source, alias_type, alias_value_sha256),
  KEY idx_graph_alias_entity (canonical_entity_uid(191)),
  KEY idx_graph_alias_search (tenant_id, scope_cluster_id, alias_type, alias_value(191)),
  CONSTRAINT chk_graph_alias_status CHECK (status IN ('active','conflict','stale','rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS hardware_assets (
  asset_uuid CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NULL,
  system_uuid VARCHAR(128) NULL,
  vendor VARCHAR(128) NOT NULL DEFAULT '',
  product_name VARCHAR(255) NOT NULL DEFAULT '',
  serial_number VARCHAR(255) NOT NULL DEFAULT '',
  hostname VARCHAR(255) NOT NULL DEFAULT '',
  bmc_identifier VARCHAR(255) NOT NULL DEFAULT '',
  inventory_hash CHAR(64) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  last_inventory_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_hw_asset_system_uuid (tenant_id, system_uuid),
  KEY idx_hw_asset_cluster (tenant_id, cluster_id),
  KEY idx_hw_asset_serial (tenant_id, serial_number(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS hardware_components (
  component_uid VARCHAR(512) PRIMARY KEY,
  asset_uuid CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  component_type VARCHAR(32) NOT NULL,
  stable_locator VARCHAR(512) NOT NULL,
  stable_locator_sha256 CHAR(64) NOT NULL,
  vendor VARCHAR(128) NOT NULL DEFAULT '',
  model VARCHAR(255) NOT NULL DEFAULT '',
  serial_number VARCHAR(255) NOT NULL DEFAULT '',
  capacity_bytes BIGINT NULL,
  pci_bdf VARCHAR(32) NOT NULL DEFAULT '',
  permanent_mac VARCHAR(64) NOT NULL DEFAULT '',
  wwn VARCHAR(255) NOT NULL DEFAULT '',
  resolution VARCHAR(16) NOT NULL DEFAULT 'physical',
  inventory_json JSON NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  last_inventory_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_hw_component_locator (asset_uuid, component_type, stable_locator_sha256),
  KEY idx_hw_component_asset (asset_uuid, component_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS business_systems (
  business_uuid CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  name VARCHAR(255) NOT NULL,
  name_key VARCHAR(255) NOT NULL,
  owner VARCHAR(255) NOT NULL DEFAULT '',
  criticality VARCHAR(16) NOT NULL DEFAULT 'normal',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version BIGINT NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_business_name (tenant_id, name_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS applications (
  application_uuid CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  business_uuid CHAR(36) NOT NULL,
  name VARCHAR(255) NOT NULL,
  name_key VARCHAR(255) NOT NULL,
  owner VARCHAR(255) NOT NULL DEFAULT '',
  criticality VARCHAR(16) NOT NULL DEFAULT 'normal',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version BIGINT NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_application_name (tenant_id, business_uuid, name_key),
  KEY idx_application_business (tenant_id, business_uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS application_services (
  service_uuid CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  application_uuid CHAR(36) NOT NULL,
  name VARCHAR(255) NOT NULL,
  name_key VARCHAR(255) NOT NULL,
  cluster_id CHAR(36) NULL,
  namespace VARCHAR(255) NOT NULL DEFAULT '',
  k8s_service_uid VARCHAR(128) NOT NULL DEFAULT '',
  telemetry_service_name VARCHAR(255) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version BIGINT NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_app_service_name (tenant_id, application_uuid, name_key),
  KEY idx_app_service_app (tenant_id, application_uuid),
  KEY idx_app_service_k8s (cluster_id, k8s_service_uid),
  KEY idx_app_service_telemetry (tenant_id, cluster_id, telemetry_service_name(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_schema_state (
  schema_name VARCHAR(64) PRIMARY KEY,
  schema_version BIGINT NOT NULL,
  schema_checksum_sha256 CHAR(64) NOT NULL,
  graphspace VARCHAR(128) NOT NULL,
  graph_name VARCHAR(128) NOT NULL,
  applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  applied_by VARCHAR(128) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_reconcile_runs (
  reconcile_run_id CHAR(36) PRIMARY KEY,
  source VARCHAR(64) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  scope_cluster_id CHAR(36) NOT NULL,
  generation BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'running',
  vertices_seen BIGINT NOT NULL DEFAULT 0,
  edges_seen BIGINT NOT NULL DEFAULT 0,
  vertices_staled BIGINT NOT NULL DEFAULT 0,
  edges_staled BIGINT NOT NULL DEFAULT 0,
  error_message VARCHAR(2048) NOT NULL DEFAULT '',
  started_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  completed_at DATETIME(3) NULL,
  KEY idx_graph_reconcile_scope (source, tenant_id, scope_cluster_id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS graph_shadow_diff_runs (
  diff_run_id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  scope_cluster_id CHAR(36) NOT NULL,
  sample_kind VARCHAR(32) NOT NULL,
  sample_count INT NOT NULL DEFAULT 0,
  mismatch_count INT NOT NULL DEFAULT 0,
  detail_json JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_graph_shadow_scope (tenant_id, scope_cluster_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS ai_run_graph_contexts (
  run_id CHAR(36) NOT NULL,
  context_version BIGINT NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  scope_kind VARCHAR(16) NOT NULL,
  primary_cluster_id CHAR(36) NULL,
  graph_schema_version BIGINT NOT NULL,
  graph_generation BIGINT NOT NULL,
  evidence_cutoff_at DATETIME(3) NOT NULL,
  trigger_entity_uid VARCHAR(512) NOT NULL,
  root_cause_entity_uid VARCHAR(512) NULL,
  is_final TINYINT NOT NULL DEFAULT 0,
  context_json JSON NOT NULL,
  context_digest_sha256 CHAR(64) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, context_version),
  KEY idx_run_graph_final (run_id, is_final, context_version),
  KEY idx_run_graph_scope (tenant_id, primary_cluster_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
