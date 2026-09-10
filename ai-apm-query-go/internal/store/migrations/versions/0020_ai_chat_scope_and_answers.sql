-- mysql/0020-ai-chat-scope-and-answers
-- Structured assistant conversations freeze the resource and absolute window
-- at first turn.  The scope is never updated on resume; answer metadata is
-- stored in the existing JSON transcript column after API validation.
ALTER TABLE ai_chat_sessions
  ADD COLUMN resource_uid VARCHAR(255) NULL AFTER cluster_id,
  ADD COLUMN time_from DATETIME(3) NULL AFTER resource_uid,
  ADD COLUMN time_to DATETIME(3) NULL AFTER time_from,
  ADD COLUMN knowledge_scope VARCHAR(64) NOT NULL DEFAULT 'platform_common_and_current_cluster' AFTER time_to,
  ADD KEY idx_ai_chat_sessions_assistant_scope (tenant_id, cluster_id, resource_uid, time_from, time_to);
-- statement-breakpoint
-- assistant answer is a JSON metadata contract (kind=answer), not a second
-- transcript table.  This keeps old assistant text replayable for two minor
-- versions while the new UI renders only validated structured answers.
