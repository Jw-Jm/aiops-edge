-- 0004_change_events.sql — P1-1 变更时间线
-- 记录运维变更/发布/配置调整/人工操作，供故障定位时关联变更时间线。
-- 归属：ai-orchestrator（迁移器自动按版本顺序执行，schema_migrations 记录去重）。
CREATE TABLE IF NOT EXISTS change_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  cluster_id VARCHAR(64) NOT NULL DEFAULT 'default',
  service VARCHAR(255) NOT NULL,
  change_type VARCHAR(32) NOT NULL,
  operator VARCHAR(128) NOT NULL,
  content TEXT NOT NULL,
  related_trace_ids TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_svc_time (cluster_id, service, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
