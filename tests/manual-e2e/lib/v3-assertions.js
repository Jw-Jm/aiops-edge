// V3 release-gate assertions (dependency free, Node >= 18).
//
// These helpers are the only accepted way to turn a live browser run into a
// PASS/FAIL verdict. They exist so a release cannot be declared publishable
// from prose, screenshots alone, or a green checkmark that was never evaluated
// against the real payload.

const FORBIDDEN_COPY = [
  /生产平台/,
  /生产集群/,
  /综合健康分/,
  /健康评分/,
  /全局搜索/,
  /\b(prod|dev|staging|test)\b\s*(环境|集群)/i,
  // 旧产品语义：合并统计与自动处置入口。
  /Pod\/Container/,
  /Service\/Ingress 合并/,
  /自动修复/,
]

/**
 * Throws when user-visible copy reintroduces a removed product layer,
 * a synthetic health score, or a global search affordance.
 */
function assertNoForbiddenProductCopy(text) {
  if (typeof text !== 'string' || text.length === 0) return
  const hit = FORBIDDEN_COPY.find((pattern) => pattern.test(text))
  if (hit) {
    const error = new Error(`forbidden product copy: ${hit}`)
    error.code = 'FORBIDDEN_PRODUCT_COPY'
    error.pattern = String(hit)
    throw error
  }
}

/**
 * Any 4xx/5xx inside a primary flow is a failure. There is no route
 * whitelist: if a page needs a resource that 404s, the flow is broken.
 */
function assertNoBadResponses(responses) {
  const bad = (responses || []).filter((response) => Number(response.status) >= 400)
  if (bad.length > 0) {
    const error = new Error(
      `bad responses in primary flow: ${bad.map((r) => `${r.status} ${r.method || 'GET'} ${r.path || r.url}`).join(', ')}`,
    )
    error.code = 'BAD_RESPONSES'
    error.responses = bad
    throw error
  }
}

/**
 * A primary cell is readable when it does not wrap character by character.
 * Long identifiers are allowed to break inside their own container only when
 * a detail/copy entry point exists.
 */
function primaryCellReadable(metric) {
  if (!metric) return false
  if (!metric.textLength) return true
  if (metric.hasCopyOrDetailEntry) return true
  if (!metric.clientWidth || metric.clientWidth <= 0) return false
  return metric.clientWidth >= 80 && Number(metric.renderedLines) <= 3
}

function assertReadablePrimaryCells(metrics) {
  const unreadable = (metrics || []).filter((metric) => !primaryCellReadable(metric))
  if (unreadable.length > 0) {
    const error = new Error(`unreadable primary cells: ${unreadable.length}`)
    error.code = 'UNREADABLE_PRIMARY_CELLS'
    error.metrics = unreadable
    throw error
  }
}

/**
 * Platform and cluster payloads must agree for the same cluster, and a stale
 * or uncovered cluster must never be reported healthy.
 */
function healthFactsConsistent(platform, detail) {
  if (!platform || !detail) return false
  if ((platform.stale || platform.covered === false) && platform.status === 'healthy') return false
  return platform.status === detail.status
}

function assertHealthFacts(overviewPayload, clusterPayload) {
  if (!healthFactsConsistent(overviewPayload, clusterPayload)) {
    const error = new Error(
      `health facts disagree: platform=${JSON.stringify(overviewPayload)} cluster=${JSON.stringify(clusterPayload)}`,
    )
    error.code = 'HEALTH_FACTS_INCONSISTENT'
    throw error
  }
}

/** Synthetic 0–100 scores and Workload aggregates must never reach the UI. */
function assertNoSyntheticHealthScore(payload) {
  const text = JSON.stringify(payload || {})
  const forbidden = ['platform_status', 'health_score', 'composite_score', 'overall_score', 'workload_total']
  const hit = forbidden.find((key) => text.includes(`"${key}"`))
  if (hit) {
    const error = new Error(`synthetic health field present: ${hit}`)
    error.code = 'SYNTHETIC_HEALTH_SCORE'
    throw error
  }
}

/** KubeVirt capability states must be distinguishable and never fabricated. */
function assertKubeVirtCapability(payload) {
  if (!payload) {
    const error = new Error('kubevirt payload missing')
    error.code = 'KUBEVIRT_PAYLOAD_MISSING'
    throw error
  }
  if (payload.installed === false && Number(payload.count) > 0) {
    const error = new Error('kubevirt reported not installed but returned VMs')
    error.code = 'KUBEVIRT_CONTRADICTORY'
    throw error
  }
}

module.exports = {
  FORBIDDEN_COPY,
  assertNoForbiddenProductCopy,
  assertNoBadResponses,
  assertReadablePrimaryCells,
  primaryCellReadable,
  healthFactsConsistent,
  assertHealthFacts,
  assertNoSyntheticHealthScore,
  assertKubeVirtCapability,
}
