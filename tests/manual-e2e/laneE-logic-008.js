const { standalone } = require('./strict/case-runner')
standalone('PF-LOGIC-008', ['action', 'action_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.action?.id), detail: 'bounded action proposal and readback' }))
