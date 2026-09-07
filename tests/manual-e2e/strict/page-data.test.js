const test = require('node:test')
const assert = require('node:assert/strict')

const { extractItems, validateTraceRows, validateForecast } = require('./page-data')

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
