const test = require('node:test')
const assert = require('node:assert/strict')

const { shapeOf, isHttpSuccess, sameIdentity } = require('./backend')

test('backend helpers describe JSON shapes and HTTP status without treating empty data as an error', () => {
  assert.equal(isHttpSuccess({ status: 200 }), true)
  assert.equal(isHttpSuccess({ status: 404 }), false)
  assert.deepEqual(shapeOf({ services: [], total: 0 }), { keys: ['services', 'total'], count: 0 })
})

test('sameIdentity requires the canonical tenant and cluster labels to agree', () => {
  assert.equal(sameIdentity({ tenant_id: 'tenant-a', cluster_id: 'cluster-a' }, 'tenant-a', 'cluster-a'), true)
  assert.equal(sameIdentity({ tenant_id: 'tenant-a', cluster_id: 'cluster-b' }, 'tenant-a', 'cluster-a'), false)
})
