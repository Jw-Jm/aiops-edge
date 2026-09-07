const test = require('node:test')
const assert = require('node:assert/strict')

const { summarizeItems } = require('./summarize')

const manifest = Array.from({ length: 87 }, (_, index) => ({ id: `PF-TEST-${String(index + 1).padStart(3, '0')}` }))

test('recomputes counts from items and ignores stale supplied counts', () => {
  const items = manifest.map((spec, index) => ({
    id: spec.id,
    status: index < 45 ? 'PASS' : index < 59 ? 'FAIL' : index < 67 ? 'PARTIAL' : 'BLOCKED',
    ...(index >= 67 ? { blocker_code: 'TELEMETRY_PRECONDITION_UNMET', evidence: { reason: 'fixture' } } : {}),
  }))

  const summary = summarizeItems({
    manifest,
    items,
    counts: { PASS: 44, FAIL: 14, PARTIAL: 9, BLOCKED: 20 },
  })

  assert.deepEqual(summary.counts, { PASS: 45, FAIL: 14, PARTIAL: 8, BLOCKED: 20, ERROR: 0 })
  assert.equal(summary.total_expected, 87)
  assert.equal(summary.total_observed, 87)
  assert.equal(summary.strict_go, false)
})

test('rejects duplicate, unknown and missing manifest IDs', () => {
  assert.throws(
    () => summarizeItems({ manifest: [{ id: 'PF-TEST-001' }], items: [{ id: 'PF-TEST-001', status: 'PASS' }, { id: 'PF-TEST-001', status: 'PASS' }] }),
    /duplicate/i,
  )
  assert.throws(
    () => summarizeItems({ manifest: [{ id: 'PF-TEST-001' }], items: [{ id: 'PF-TEST-999', status: 'PASS' }] }),
    /unknown/i,
  )
  assert.throws(
    () => summarizeItems({ manifest: [{ id: 'PF-TEST-001' }, { id: 'PF-TEST-002' }], items: [{ id: 'PF-TEST-001', status: 'PASS' }] }),
    /missing/i,
  )
})

test('strict_go is true only when every manifest item is PASS', () => {
  const summary = summarizeItems({
    manifest: [{ id: 'PF-TEST-001' }, { id: 'PF-TEST-002' }],
    items: [{ id: 'PF-TEST-001', status: 'PASS' }, { id: 'PF-TEST-002', status: 'PASS' }],
  })

  assert.equal(summary.strict_go, true)
})
