const { standalone } = require('./strict/case-runner')
standalone('PF-AI-003', ['tool_runs', 'evidence'], (ledger) => ({ pass: ledger.artifacts.tool_runs?.status === 200 && ledger.artifacts.evidence?.status === 200, detail: 'ToolRun and Evidence projections' }))
