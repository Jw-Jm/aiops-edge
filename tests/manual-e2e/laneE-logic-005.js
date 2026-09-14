const { standalone } = require('./strict/case-runner')
standalone('PF-LOGIC-005', ['chat_turn_1', 'chat_turn_2', 'chat_session_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.chat_session?.session_id), detail: 'two real Chat turns and session readback' }))
