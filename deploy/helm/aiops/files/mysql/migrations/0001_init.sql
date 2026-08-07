-- 0001_init.sql: 业务状态库最小初始化（P1b 正式表结构前先建库）
CREATE DATABASE IF NOT EXISTS aiops;
USE aiops;
CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(255) PRIMARY KEY,
  applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT IGNORE INTO schema_migrations (version) VALUES ('0001_init');
