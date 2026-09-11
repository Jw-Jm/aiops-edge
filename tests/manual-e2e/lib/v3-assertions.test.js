const test = require('node:test')
const assert = require('node:assert/strict')
const {
  assertNoForbiddenProductCopy,
  assertNoBadResponses,
  assertReadablePrimaryCells,
  primaryCellReadable,
  healthFactsConsistent,
  assertHealthFacts,
  assertNoSyntheticHealthScore,
  assertKubeVirtCapability,
} = require('./v3-assertions')

test('rejects removed product layers and synthetic scores', () => {
  assert.throws(() => assertNoForbiddenProductCopy('生产平台 综合健康分数 92'))
  assert.throws(() => assertNoForbiddenProductCopy('生产集群'))
  assert.throws(() => assertNoForbiddenProductCopy('请使用全局搜索'))
  assert.throws(() => assertNoForbiddenProductCopy('切换 dev 环境'))
  assert.doesNotThrow(() => assertNoForbiddenProductCopy('云平台运营态势 集群 上海一号'))
  // 合并统计与自动处置文案同样被拒绝。
  assert.throws(() => assertNoForbiddenProductCopy('Pod/Container 合并计数'))
  assert.throws(() => assertNoForbiddenProductCopy('点击自动修复'))
})

test('rejects any 4xx/5xx inside a primary flow', () => {
  assert.throws(() => assertNoBadResponses([{ status: 403, method: 'GET', path: '/api/v1/alerts/aggregation' }]))
  assert.throws(() => assertNoBadResponses([{ status: 502, path: '/api/v1/resources/catalog' }]))
  assert.doesNotThrow(() => assertNoBadResponses([{ status: 200, path: '/api/v1/resources/catalog' }]))
  assert.doesNotThrow(() => assertNoBadResponses([]))
})

test('rejects stale healthy facts', () => {
  assert.equal(healthFactsConsistent({ status: 'healthy', stale: true, covered: false }, { status: 'unknown' }), false)
  assert.equal(healthFactsConsistent({ status: 'healthy', stale: false, covered: true }, { status: 'healthy' }), true)
  assert.equal(healthFactsConsistent({ status: 'degraded', stale: false, covered: true }, { status: 'degraded' }), true)
  assert.equal(healthFactsConsistent({ status: 'healthy', stale: false, covered: true }, { status: 'degraded' }), false)
  assert.throws(() => assertHealthFacts({ status: 'healthy', stale: true }, { status: 'unknown' }))
})

test('rejects one-character wrapping in a primary cell', () => {
  assert.equal(primaryCellReadable({ textLength: 36, renderedLines: 18, clientWidth: 24 }), false)
  assert.equal(primaryCellReadable({ textLength: 36, renderedLines: 1, clientWidth: 220 }), true)
  // 长 UID 在自身容器断词但提供了复制/详情入口 → 允许。
  assert.equal(primaryCellReadable({ textLength: 36, renderedLines: 6, clientWidth: 90, hasCopyOrDetailEntry: true }), true)
  assert.throws(() => assertReadablePrimaryCells([{ textLength: 36, renderedLines: 18, clientWidth: 24 }]))
  assert.doesNotThrow(() => assertReadablePrimaryCells([{ textLength: 10, renderedLines: 1, clientWidth: 200 }]))
})

test('rejects synthetic health fields and contradictory kubevirt capability', () => {
  assert.throws(() => assertNoSyntheticHealthScore({ platform_status: 'healthy' }))
  assert.throws(() => assertNoSyntheticHealthScore({ health_score: 92 }))
  assert.doesNotThrow(() => assertNoSyntheticHealthScore({ managed_clusters: 2, cluster_states: { healthy: 1 } }))
  assert.throws(() => assertKubeVirtCapability({ installed: false, count: 3 }))
  assert.doesNotThrow(() => assertKubeVirtCapability({ installed: false, count: 0 }))
  assert.doesNotThrow(() => assertKubeVirtCapability({ installed: true, count: 0 }))
})

test('acceptance runner cannot start critical gates green or accept an empty graph shell', () => {
  const fs = require('node:fs')
  const source = fs.readFileSync(require('node:path').join(__dirname, '..', 'v3-acceptance.js'), 'utf8')
  assert.match(source, /cluster_isolation:\s*'FAIL'/)
  assert.match(source, /graph_semantics:\s*'FAIL'/)
  assert.match(source, /press\('Enter'\)/)
  assert.match(source, /relationButtons/)
  assert.match(source, /waitForRouteSettled/)
  assert.doesNotMatch(source, /cluster_isolation:\s*'PASS'/)
  assert.doesNotMatch(source, /graph_semantics:\s*'PASS',/)
})
