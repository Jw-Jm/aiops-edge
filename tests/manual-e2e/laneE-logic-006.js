const { standalone } = require('./strict/case-runner')
standalone('PF-LOGIC-006', ['run', 'run_readback', 'tool_runs', 'evidence'], (ledger) => ({ pass: ledger.artifacts.run?.id && ledger.artifacts.tool_runs?.status === 200 && ledger.artifacts.evidence?.status === 200, detail: 'durable Run and ToolRun evidence' }))
