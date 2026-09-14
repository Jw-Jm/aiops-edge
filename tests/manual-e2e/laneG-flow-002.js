const { standalone } = require('./strict/case-runner')
standalone('PF-FLOW-002', ['chat_turn_1', 'chat_turn_2', 'run_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.chat_session?.session_id && ledger.artifacts.run_readback?.id), detail: 'Chat to investigation flow' }))
