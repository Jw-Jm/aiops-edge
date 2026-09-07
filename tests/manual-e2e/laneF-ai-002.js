const { standalone } = require('./strict/case-runner')
standalone('PF-AI-002', ['run', 'run_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.run_readback?.id), detail: 'durable investigation run' }))
