// Lane A (PF-PAGE-002..013) helper: run a page test and append the summary to
// the laneA-summary.jsonl file required by the manual's non-LLM phase.
// Canonical location per orchestrator: tests/test-results/<RUN_ID>/pages/.
// The shared harness ROOT resolves to <repo>/test-results/<RUN_ID>/ (merged
// with other lanes), so per-page JSONs are mirrored here as well.
const fs = require('fs')
const path = require('path')
const { ROOT, runPageTest } = require('./harness')

const CANONICAL = path.resolve(__dirname, '..', '..', 'test-results', process.env.TEST_RUN_ID || 'nonllm-20260905', 'pages')

async function runLaneA(opts) {
  const summary = await runPageTest(opts)
  fs.mkdirSync(CANONICAL, { recursive: true })
  const line = JSON.stringify(summary) + '\n'
  fs.appendFileSync(path.join(CANONICAL, 'laneA-summary.jsonl'), line)
  // Durable backup: concurrent lanes may clean shared run directories.
  fs.appendFileSync('/tmp/laneA-summary-backup.jsonl', line)
  // Mirror the detailed per-page JSON (written by the harness under ROOT).
  try {
    const src = path.join(ROOT, 'pages', `${opts.id}.json`)
    fs.copyFileSync(src, path.join(CANONICAL, `${opts.id}.json`))
  } catch { /* harness JSON missing; summary still recorded */ }
  console.log(line.trim())
  return summary
}

module.exports = { runLaneA }
