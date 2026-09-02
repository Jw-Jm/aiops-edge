-- mysql/0017-ai-chat-tool-runs
-- Durable audit for ordinary Chat read-only tool calls.  This table is not an
-- Investigation Run and deliberately has no run_id/evidence relationship.
CREATE TABLE IF NOT EXISTS ai_chat_tool_runs (
  chat_tool_run_id CHAR(36) PRIMARY KEY,
  principal_id CHAR(36) NOT NULL,
  session_id CHAR(36) NOT NULL,
  chat_session_id CHAR(36) NOT NULL,
  turn_id CHAR(36) NOT NULL,
  tool_call_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  tool_name VARCHAR(128) NOT NULL,
  operation VARCHAR(64) NOT NULL,
  capability VARCHAR(128) NOT NULL,
  args_hash CHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'running',
  result_digest_sha256 CHAR(64) NULL,
  result_count BIGINT NOT NULL DEFAULT 0,
  error_code VARCHAR(64) NULL,
  started_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  completed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_chat_tool_call (chat_session_id, turn_id, tool_call_id),
  KEY idx_ai_chat_tool_scope (tenant_id, cluster_id, created_at),
  KEY idx_ai_chat_tool_identity (principal_id, session_id, created_at),
  CONSTRAINT fk_ai_chat_tool_user FOREIGN KEY (principal_id) REFERENCES users(user_uuid),
  CONSTRAINT fk_ai_chat_tool_session FOREIGN KEY (session_id) REFERENCES auth_sessions(session_id),
  CONSTRAINT fk_ai_chat_tool_chat_session FOREIGN KEY (chat_session_id) REFERENCES ai_chat_sessions(session_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
