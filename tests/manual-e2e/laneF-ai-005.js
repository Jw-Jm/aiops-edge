const { standalone } = require('./strict/case-runner')
standalone('PF-AI-005', ['run_readback', 'evidence'], (ledger) => ({ pass: ledger.artifacts.evidence?.status === 200, detail: 'RCA evidence is sourced from the Run' }))
