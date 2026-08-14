-- 0003_reports_llm_mode.sql — reports 表增加 llm_mode 列
-- 记录报告生成时的 LLM 模式（llm=真实 AI 推理 / deterministic=确定性模板），
-- 供 /api/v1/ops/reports/history 响应返回 llm_mode 字段。
-- 幂等：列已存在时跳过（ALTER TABLE ADD COLUMN 不支持 IF NOT EXISTS）。
SET @has_col := (SELECT COUNT(*) FROM information_schema.COLUMNS
                 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reports' AND COLUMN_NAME = 'llm_mode');
SET @sql := IF(@has_col = 0,
    'ALTER TABLE reports ADD COLUMN llm_mode VARCHAR(16) DEFAULT ''llm'' NULL',
    'SELECT 1');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
