const test = require('node:test')
const assert = require('node:assert/strict')

const { telemetryBlocker, traceHasParentChild, isFreshTelemetry } = require('./telemetry-cases')

test('telemetryBlocker creates one stable shared precondition evidence object', () => {
  const evidence = telemetryBlocker({ marker: 'strict-1', reason: 'timeout', traceCount: 0 })
  assert.equal(evidence.blocker_code, 'TELEMETRY_PRECONDITION_UNMET')
  assert.equal(evidence.marker, 'strict-1')
  assert.equal(evidence.reason, 'timeout')
})

test('traceHasParentChild validates the generated trace relation', () => {
  assert.equal(traceHasParentChild({ spans: [
    { span_id: 'root', parent_span_id: '' },
    { span_id: 'child', parent_span_id: 'root' },
  ] }), true)
  assert.equal(traceHasParentChild({ spans: [{ span_id: 'child', parent_span_id: 'missing' }] }), false)
})

test('isFreshTelemetry requires a marker and a recent timestamp', () => {
  const now = Date.now()
  assert.equal(isFreshTelemetry({ marker: 'strict-1', timestamp: new Date(now - 1000).toISOString() }, 'strict-1', now), true)
  assert.equal(isFreshTelemetry({ marker: 'other', timestamp: new Date(now - 1000).toISOString() }, 'strict-1', now), false)
  assert.equal(isFreshTelemetry({ marker: 'strict-1', timestamp: new Date(now - 25 * 60 * 60 * 1000).toISOString() }, 'strict-1', now), false)
})
