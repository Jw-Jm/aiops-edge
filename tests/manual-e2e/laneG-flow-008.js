const { standalone } = require('./strict/case-runner')
standalone('PF-FLOW-008', ['report', 'knowledge', 'knowledge_readback'], (ledger) => ({ pass: ledger.artifacts.knowledge_readback?.status === 200, detail: 'report to knowledge to later-read flow' }))
