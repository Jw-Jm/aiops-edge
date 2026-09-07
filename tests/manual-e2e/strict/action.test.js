const test = require('node:test')
const assert = require('node:assert/strict')

const { actionExecutionSucceeded, actionStateMatches } = require('./action')

test('actionExecutionSucceeded requires a successful public execution state', () => {
  assert.equal(actionExecutionSucceeded({ status: 200, body: { status: 'success', execution_status: 'success' } }), true)
  assert.equal(actionExecutionSucceeded({ status: 200, body: { status: 'rejected', error_code: 'EXECUTOR_REJECTED' } }), false)
  assert.equal(actionExecutionSucceeded({ status: 202, body: { status: 'executing' } }), false)
})

test('actionStateMatches compares the observed deployment replica count to the requested state', () => {
  assert.equal(actionStateMatches('2', 2), true)
  assert.equal(actionStateMatches('1', 2), false)
  assert.equal(actionStateMatches('', 1), false)
})
