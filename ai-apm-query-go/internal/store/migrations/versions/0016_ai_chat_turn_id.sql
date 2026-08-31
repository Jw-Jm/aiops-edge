-- mysql/0016-ai-chat-turn-id
-- Bind each durable chat card to a client/server generated turn.  The
-- nullable column preserves legacy transcripts; new canonical traffic always
-- supplies a UUID and is protected against duplicate retry inserts.
ALTER TABLE ai_chat_messages ADD COLUMN turn_id CHAR(36) NULL AFTER session_id;
-- statement-breakpoint
ALTER TABLE ai_chat_messages ADD UNIQUE KEY uq_ai_chat_message_turn (session_id, turn_id, role, kind);
