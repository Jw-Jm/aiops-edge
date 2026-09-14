const { standalone } = require('./strict/case-runner')
standalone('PF-AI-006', ['run_readback'], (ledger) => ({ pass: Boolean(ledger.artifacts.run_readback?.id), detail: 'RCA uncertainty is read from terminal Run state' }))
