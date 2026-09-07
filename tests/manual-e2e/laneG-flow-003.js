const { standalone } = require('./strict/case-runner')
standalone('PF-FLOW-003', ['run_readback', 'tool_runs', 'evidence', 'graph_context'], (ledger) => ({ pass: ledger.artifacts.graph_context?.status === 200, detail: 'investigation evidence flow' }))
