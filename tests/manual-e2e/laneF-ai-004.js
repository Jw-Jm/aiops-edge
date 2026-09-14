const { standalone } = require('./strict/case-runner')
standalone('PF-AI-004', ['run_readback', 'graph_context'], (ledger) => ({ pass: ledger.artifacts.graph_context?.status === 200, detail: 'Graph Context projection' }))
