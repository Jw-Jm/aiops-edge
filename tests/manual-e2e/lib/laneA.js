// Lane A (PF-PAGE-002..013) helper: run a page test and append the summary to
// the laneA-summary.jsonl file required by the manual's non-LLM phase.
// Canonical location per orchestrator: tests/test-results/<RUN_ID>/pages/.
// The shared harness ROOT resolves to <repo>/test-results/<RUN_ID>/ (merged
// with other lanes), so per-page JSONs are mirrored here as well.
const fs = require('fs')
const path = require('path')
const { ROOT, runPageTest } = require('./harness')

async function runLaneA(opts) {
  const summary = await runPageTest(opts)
  const canonical = path.join(ROOT, 'pages')
  fs.mkdirSync(canonical, { recursive: true })
  const line = JSON.stringify(summary) + '\n'
  fs.appendFileSync(path.join(canonical, 'laneA-summary.jsonl'), line)
  console.log(line.trim())
  return summary
}

module.exports = { runLaneA }
