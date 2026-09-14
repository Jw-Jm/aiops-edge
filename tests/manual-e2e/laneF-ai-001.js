const { standalone } = require('./strict/case-runner')
standalone('PF-AI-001', ['chat_turn_1', 'chat_turn_2', 'chat_session_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.chat_session?.session_id), detail: 'real LLM Chat session' }))
