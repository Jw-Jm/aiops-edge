const { standalone } = require('./strict/case-runner')
standalone('PF-LOGIC-011', ['report', 'knowledge', 'knowledge_readback'], (ledger) => ({ pass: ledger.artifacts.knowledge_readback?.status === 200, detail: 'report-backed knowledge entry and public readback' }))
