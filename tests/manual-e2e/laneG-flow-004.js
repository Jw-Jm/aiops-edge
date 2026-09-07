const { standalone } = require('./strict/case-runner')
standalone('PF-FLOW-004', ['action', 'action_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.action_readback?.id), detail: 'bounded action approval flow' }))
