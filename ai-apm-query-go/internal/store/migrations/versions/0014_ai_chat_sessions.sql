-- mysql/0014-ai-chat-sessions
-- Query API is the sole owner of browser chat session metadata and messages.
CREATE TABLE IF NOT EXISTS ai_chat_sessions (
  session_id CHAR(36) PRIMARY KEY,
  user_uuid CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  intent VARCHAR(128) NOT NULL DEFAULT 'diagnosis',
  service VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_ai_chat_sessions_scope (user_uuid, tenant_id, cluster_id, updated_at),
  CONSTRAINT fk_ai_chat_sessions_user FOREIGN KEY (user_uuid) REFERENCES users(user_uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS ai_chat_messages (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  session_id CHAR(36) NOT NULL,
  role VARCHAR(16) NOT NULL,
  kind VARCHAR(32) NOT NULL DEFAULT '',
  content LONGTEXT NOT NULL,
  metadata_json JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_ai_chat_messages_session (session_id, id),
  CONSTRAINT fk_ai_chat_messages_session FOREIGN KEY (session_id) REFERENCES ai_chat_sessions(session_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
