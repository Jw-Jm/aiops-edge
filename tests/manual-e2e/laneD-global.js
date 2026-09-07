// Lane D global UI tests (PF-UI-001~006) — entry point.
// Order matters: PF-UI-004 (logout) MUST run last because it clears the session.
// Usage: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneD-global.js
const fs = require('fs')
const path = require('path')
const { ROOT, ensureDirs } = require('./lib/laneD-runner')
const t001 = require('./laneD-001-nav')
const t002 = require('./laneD-002-cluster')
const t003 = require('./laneD-003-alerts')
const t005 = require('./laneD-005-pages')
const t004 = require('./laneD-004-user')

;(async () => {
  const results = {}
  results['PF-UI-001'] = await t001.run()
  results['PF-UI-002'] = await t002.run()
  results['PF-UI-003'] = await t003.run()
  const pages = await t005.run()
  results['PF-UI-005'] = pages.s5
  results['PF-UI-006'] = pages.s6
  results['PF-UI-004'] = await t004.run() // last: logout clears the session

  ensureDirs()
  const overall = (id) => results[id]?.status || 'UNKNOWN'
  const summary = {
    lane: 'D',
    scope: '全局 UI（PF-UI-001~006）',
    generatedAt: new Date().toISOString(),
    results,
    overall: ['PF-UI-001', 'PF-UI-002', 'PF-UI-003', 'PF-UI-004', 'PF-UI-005', 'PF-UI-006']
      .map((id) => `${id}=${overall(id)}`).join(' '),
    keyFindings: [
      ...(results['PF-UI-004']?.failedChecks || []).length
        ? [`PF-UI-004 failed: ${results['PF-UI-004'].failedChecks.join(', ')}`] : [],
      ...(results['PF-UI-005']?.failedChecks || []).length
        ? [`PF-UI-005 failed: ${results['PF-UI-005'].failedChecks.slice(0, 10).join(', ')}`] : [],
      ...(results['PF-UI-006']?.failedChecks || []).length
        ? [`PF-UI-006 failed: ${results['PF-UI-006'].failedChecks.join(', ')}`] : [],
    ],
  }
  fs.writeFileSync(path.join(ROOT, 'pages', 'laneD-summary.json'), JSON.stringify(summary, null, 2))
  console.log('LANE-D-SUMMARY:', JSON.stringify(summary.overall))
})().catch((e) => { console.error(e); process.exit(1) })
