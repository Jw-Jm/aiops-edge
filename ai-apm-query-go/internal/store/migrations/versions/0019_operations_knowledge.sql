-- mysql/0019-operations-knowledge
-- Authoritative operations knowledge source-of-truth and rebuildable index outbox.
CREATE TABLE IF NOT EXISTS operations_knowledge (
  knowledge_id CHAR(36) PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL,
  scope_type VARCHAR(32) NOT NULL,
  cluster_id CHAR(36) NULL,
  knowledge_type VARCHAR(32) NOT NULL,
  title VARCHAR(255) NOT NULL,
  summary TEXT NOT NULL,
  source_kind VARCHAR(32) NOT NULL DEFAULT 'manual',
  source_revision VARCHAR(255) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  current_version_id CHAR(36) NULL,
  draft_version_id CHAR(36) NULL,
  created_by VARCHAR(128) NOT NULL,
  updated_by VARCHAR(128) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  disabled_at DATETIME(3) NULL,
  CONSTRAINT chk_operations_knowledge_scope CHECK (scope_type IN ('platform_common','cluster')),
  CONSTRAINT chk_operations_knowledge_status CHECK (status IN ('draft','pending_review','published','disabled')),
  CONSTRAINT chk_operations_knowledge_cluster CHECK ((scope_type='platform_common' AND cluster_id IS NULL) OR (scope_type='cluster' AND cluster_id IS NOT NULL)),
  INDEX ix_operations_knowledge_scope (tenant_id, scope_type, cluster_id, status),
  INDEX ix_operations_knowledge_type (tenant_id, knowledge_type, status)
);
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS operations_knowledge_versions (
  version_id CHAR(36) PRIMARY KEY,
  knowledge_id CHAR(36) NOT NULL,
  version_no INT NOT NULL,
  content LONGTEXT NOT NULL,
  content_checksum CHAR(64) NOT NULL,
  source_kind VARCHAR(32) NOT NULL DEFAULT 'manual',
  source_revision VARCHAR(255) NULL,
  created_by VARCHAR(128) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_operations_knowledge_version (knowledge_id, version_no),
  CONSTRAINT fk_operations_knowledge_version FOREIGN KEY (knowledge_id) REFERENCES operations_knowledge(knowledge_id)
);
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS operations_knowledge_reviews (
  review_id BIGINT PRIMARY KEY AUTO_INCREMENT,
  knowledge_id CHAR(36) NOT NULL,
  version_id CHAR(36) NOT NULL,
  reviewer_id VARCHAR(128) NOT NULL,
  decision VARCHAR(32) NOT NULL,
  reason TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_operations_knowledge_review (knowledge_id, version_id, reviewer_id, decision),
  CONSTRAINT chk_operations_knowledge_review_decision CHECK (decision IN ('approve','reject')),
  CONSTRAINT fk_operations_knowledge_review FOREIGN KEY (knowledge_id) REFERENCES operations_knowledge(knowledge_id),
  CONSTRAINT fk_operations_knowledge_review_version FOREIGN KEY (version_id) REFERENCES operations_knowledge_versions(version_id)
);
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS operations_knowledge_index_outbox (
  outbox_id CHAR(36) PRIMARY KEY,
  knowledge_id CHAR(36) NOT NULL,
  version_id CHAR(36) NOT NULL,
  event_kind VARCHAR(32) NOT NULL DEFAULT 'publish',
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  attempt INT NOT NULL DEFAULT 0,
  last_error VARCHAR(1024) NULL,
  next_retry_at DATETIME(3) NULL,
  indexed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_operations_knowledge_index_event (knowledge_id, version_id, event_kind),
  INDEX ix_operations_knowledge_index_pending (status, next_retry_at),
  CONSTRAINT fk_operations_knowledge_index FOREIGN KEY (knowledge_id) REFERENCES operations_knowledge(knowledge_id),
  CONSTRAINT fk_operations_knowledge_index_version FOREIGN KEY (version_id) REFERENCES operations_knowledge_versions(version_id)
);
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS operations_knowledge_index_state (
  knowledge_id CHAR(36) NOT NULL,
  version_id CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  attempt INT NOT NULL DEFAULT 0,
  last_error VARCHAR(1024) NULL,
  indexed_at DATETIME(3) NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (knowledge_id, version_id),
  CONSTRAINT fk_operations_knowledge_index_state FOREIGN KEY (knowledge_id) REFERENCES operations_knowledge(knowledge_id),
  CONSTRAINT fk_operations_knowledge_index_state_version FOREIGN KEY (version_id) REFERENCES operations_knowledge_versions(version_id)
);
