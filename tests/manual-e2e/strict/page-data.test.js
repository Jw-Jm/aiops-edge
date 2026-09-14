const test = require('node:test')
const assert = require('node:assert/strict')

const { extractItems, validateTraceRows, validateForecast, traceIdForMarker, hasStrictTraceEvidence, hasStrictServiceEvidence } = require('./page-data')

test('extractItems reads the product envelopes without inventing rows', () => {
  assert.deepEqual(extractItems({ data: [{ id: 'a' }], total: 1 }), [{ id: 'a' }])
  assert.deepEqual(extractItems({ services: [{ name: 'payments' }] }), [{ name: 'payments' }])
  assert.deepEqual(extractItems({ data: null, total: 0 }), [])
})

test('validateTraceRows requires IDs, non-negative durations and explainable parent links', () => {
  const valid = validateTraceRows([{
    trace_id: 'trace-1',
    spans: [
      { span_id: 'root', parent_span_id: '', duration_ns: 10 },
      { span_id: 'child', parent_span_id: 'root', duration_ns: 5 },
    ],
  }])
  assert.equal(valid.ok, true)
  assert.equal(validateTraceRows([{ trace_id: '', spans: [] }]).ok, false)
})

test('validateForecast accepts the documented empty-history response only when it is a 200 shape', () => {
  assert.equal(validateForecast({ status: 200, body: { history: [], timestamps: [], forecasts: {} } }).ok, true)
  assert.equal(validateForecast({ status: 400, body: { error: 'metric must be cpu' } }).ok, false)
})

test('validateForecast accepts the documented history plus forecast timeline', () => {
  const response = {
    status: 200,
    body: {
      history: [10, 11, 12],
      timestamps: [1, 2, 3, 4, 5],
      forecasts: {
        linear: { values: [13, 14] },
        ewma: { values: [12.5, 13] },
      },
    },
  }
  assert.equal(validateForecast(response).ok, true)
  assert.equal(validateForecast({
    status: 200,
    body: { ...response.body, timestamps: [1, 2, 3, 4] },
  }).ok, false)
})

test('strict telemetry evidence uses canonical service fields and deterministic trace IDs', () => {
  const marker = 'strict-marker'
  const traceId = traceIdForMarker(marker)
  assert.match(traceId, /^[a-f0-9]{64}$/)
  assert.equal(hasStrictServiceEvidence({ services: [{ service_name: 'payments' }, { service_name: 'orders' }] }, marker), true)
  assert.equal(hasStrictTraceEvidence({ data: [{ trace_id: traceId }] }, marker), true)
  assert.equal(hasStrictTraceEvidence({ data: [{ trace_id: 'other' }] }, marker), false)
})
