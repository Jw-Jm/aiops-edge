const fs = require('fs')
const path = require('path')
const { MANIFEST } = require('./manifest')
const { classifyCase } = require('./result-policy')
const { ensureStrictChain, chainEvidence } = require('../lib/strict-chain')
const { ENV, ROOT, withSession, makeCollector } = require('../lib/laneD-runner')

async function runStrictCase(id, artifacts, extraCheck = () => ({ pass: true, detail: 'shared strict evidence chain' })) {
  const vp = ENV.viewports[0]
  const checks = []
  const col = makeCollector(`${vp.width}x${vp.height}`)
  let ledger = null
  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    ledger = await ensureStrictChain(page.request)
    const telemetryEvidence = ledger.telemetry || { marker: ledger.marker }
    const telemetryReady = ledger.telemetry_ready !== false
    checks.push({ name: 'shared_chain_telemetry', pass: telemetryReady, detail: JSON.stringify(telemetryEvidence).slice(0, 500) })
    checks.push({ name: 'required_real_artifacts', pass: chainEvidence(ledger, artifacts), detail: artifacts.join(',') })
    checks.push({ name: 'artifact_readback', pass: ledger.checks?.some((item) => item.name.endsWith('readback') && item.pass) || artifacts.length === 0, detail: 'public API readback' })
    checks.push({ name: 'case_specific_assertion', ...extraCheck(ledger) })
  })
  const spec = MANIFEST.find((item) => item.id === id)
  const result = classifyCase({ id, checks, spec, preflight: { telemetryReady: ledger?.telemetry_ready !== false, telemetryEvidence: ledger?.telemetry || { marker: ledger?.marker } } })
  if (result.status === 'PASS' && checks.some((check) => !check.pass)) result.status = 'FAIL'
  if (!ledger?.telemetry_ready) {
    result.status = 'BLOCKED'
    result.blocker_code = 'TELEMETRY_PRECONDITION_UNMET'
    result.evidence = ledger?.telemetry || { reason: 'fresh strict telemetry is unavailable' }
  }
  const dir = path.join(ROOT, 'ai-chain')
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, `${id}.json`), `${JSON.stringify({ ...result, ledger_marker: ledger?.marker }, null, 2)}\n`)
  console.log(JSON.stringify({ id, status: result.status, failedChecks: result.failedChecks || [] }))
  return result
}

async function standalone(id, artifacts, extraCheck) {
  try { await runStrictCase(id, artifacts, extraCheck) } catch (error) {
    const dir = path.join(ROOT, 'ai-chain')
    fs.mkdirSync(dir, { recursive: true })
    const result = { id, status: 'ERROR', failedChecks: ['runner'], error: String(error).slice(0, 500) }
    fs.writeFileSync(path.join(dir, `${id}.json`), `${JSON.stringify(result, null, 2)}\n`)
    console.error(error)
    process.exitCode = 1
  }
}

module.exports = { runStrictCase, standalone }
