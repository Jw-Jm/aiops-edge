const test = require('node:test')
const assert = require('node:assert/strict')

const {
  classifyCase,
  applyTelemetryPrecondition,
} = require('./result-policy')

const telemetryCase = (id, overrides = {}) => ({
  id,
  requires: { telemetry: true },
  allowEmpty: false,
  ...overrides,
})

test('classifies an assertion failure as FAIL when required preconditions are ready', () => {
  const result = classifyCase({
    id: 'PF-DATA-001',
    checks: [{ name: 'trend_matches_api', pass: false, detail: 'mismatch' }],
    spec: telemetryCase('PF-DATA-001'),
    preflight: { telemetryReady: true },
  })

  assert.equal(result.status, 'FAIL')
  assert.deepEqual(result.failedChecks, ['trend_matches_api'])
  assert.equal(result.blocker_code, undefined)
})

test('classifies missing telemetry as a precondition BLOCKED result', () => {
  const result = classifyCase({
    id: 'PF-PAGE-003',
    checks: [{ name: 'empty_state_visible', pass: true }],
    spec: telemetryCase('PF-PAGE-003'),
    preflight: {
      telemetryReady: false,
      telemetryEvidence: { reason: '24h window is empty', traceCount: 0 },
    },
  })

  assert.equal(result.status, 'BLOCKED')
  assert.equal(result.blocker_code, 'TELEMETRY_PRECONDITION_UNMET')
  assert.deepEqual(result.evidence, { reason: '24h window is empty', traceCount: 0 })
})

test('does not let a correctly rendered Empty state pass a data-dependent page case', () => {
  const result = classifyCase({
    id: 'PF-PAGE-006',
    checks: [{ name: 'empty_state_visible', pass: true }],
    spec: telemetryCase('PF-PAGE-006', { allowEmpty: false }),
    preflight: { telemetryReady: false },
  })

  assert.equal(result.status, 'BLOCKED')
  assert.equal(result.blocker_code, 'TELEMETRY_PRECONDITION_UNMET')
})

test('allows an explicitly documented Empty case to pass after all checks pass', () => {
  const result = classifyCase({
    id: 'PF-DATA-008',
    checks: [
      { name: 'capacity_api_200', pass: true },
      { name: 'empty_history_rendered', pass: true },
    ],
    spec: telemetryCase('PF-DATA-008', { allowEmpty: true }),
    preflight: { telemetryReady: false },
  })

  assert.equal(result.status, 'PASS')
})

test('reclassifies all telemetry-dependent results with one shared blocker evidence object', () => {
  const results = [
    'PF-PAGE-003',
    'PF-PAGE-006',
    'PF-LOGIC-003',
    'PF-DATA-001',
    'PF-DATA-002',
    'PF-DATA-003',
    'PF-BE-004',
    'PF-FLOW-001',
  ].map((id) => ({ id, status: 'FAIL', checks: [] }))
  const evidence = { reason: 'telemetry preparation timed out', runMarker: 'strict-test' }

  const updated = applyTelemetryPrecondition(results, {
    telemetryReady: false,
    telemetryEvidence: evidence,
  })

  assert.equal(updated.length, 8)
  assert.deepEqual([...new Set(updated.map((item) => item.status))], ['BLOCKED'])
  assert.deepEqual([...new Set(updated.map((item) => item.blocker_code))], ['TELEMETRY_PRECONDITION_UNMET'])
  assert.ok(updated.every((item) => item.evidence === evidence))
})
