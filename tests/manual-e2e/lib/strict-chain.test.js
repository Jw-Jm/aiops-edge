const test = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('crypto')

const { evaluateTelemetryReadiness } = require('./strict-chain')

test('evaluateTelemetryReadiness accepts production OTLP evidence without marker attributes in service rows', () => {
  const marker = 'strict-run-marker'
  const expectedTraceID = crypto.createHash('sha256').update(`${marker}/1`).digest('hex')
  const evidence = evaluateTelemetryReadiness({
    marker,
    stats: { status: 200, body: { trend: [{ ts: 1, calls: 1 }] } },
    traces: { status: 200, body: { data: [{ trace_id: expectedTraceID, service: 'payments' }] } },
    logs: { status: 200, body: { data: [{ message: `request processed marker=${marker}` }] } },
    services: { status: 200, body: { services: [{ service: 'payments' }, { service: 'orders' }] } },
    serviceMap: { status: 200, body: { services: ['payments', 'orders'], edges: [{ source: 'payments', target: 'orders' }] } },
  })

  assert.equal(evidence.ready, true)
  assert.equal(evidence.marker_found, true)
  assert.equal(evidence.expected_trace_id, expectedTraceID)
})

test('evaluateTelemetryReadiness rejects missing marker evidence', () => {
  const evidence = evaluateTelemetryReadiness({
    marker: 'strict-missing',
    stats: { status: 200, body: { trend: [{ ts: 1, calls: 1 }] } },
    traces: { status: 200, body: { data: [{ trace_id: 'other' }] } },
    logs: { status: 200, body: { data: [{ message: 'unrelated' }] } },
    services: { status: 200, body: { services: [{ service: 'payments' }, { service: 'orders' }] } },
    serviceMap: { status: 200, body: { services: ['payments', 'orders'], edges: [{ source: 'payments', target: 'orders' }] } },
  })

  assert.equal(evidence.ready, false)
  assert.equal(evidence.marker_found, false)
})
