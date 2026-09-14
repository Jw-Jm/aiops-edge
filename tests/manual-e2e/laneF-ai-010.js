const { standalone } = require('./strict/case-runner')
standalone('PF-AI-010', ['report', 'knowledge', 'knowledge_readback'], (ledger) => ({ pass: ledger.artifacts.knowledge_readback?.status === 200, detail: 'follow-up knowledge readback' }))
