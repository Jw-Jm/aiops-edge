// V3 release gate: real login, real API, real DOM, fixed viewports.
//
// This runner is the only producer of test-results/<RUN_ID>/v3-acceptance.json.
// It refuses to report PASS when version binding, a primary route, a health
// fact, or a screenshot cannot be verified. Nothing here may be satisfied from
// a previous run: every check below is evaluated against the live deployment.
const { execSync } = require('child_process')
const fs = require('fs')
const path = require('path')
const { ENV, ROOT, makeCollector, spaNav, writeResult, shot, withSession } = require('./lib/laneD-runner')
const {
  assertNoForbiddenProductCopy,
  assertNoBadResponses,
  assertReadablePrimaryCells,
  assertHealthFacts,
  assertNoSyntheticHealthScore,
  assertKubeVirtCapability,
} = require('./lib/v3-assertions')

const ALL_ROUTES = [
  { key: 'overview', path: () => '/overview', label: '云平台运营态势' },
  { key: 'cluster', path: (c) => `/clusters/${c}`, label: '集群详细总览' },
  { key: 'resources', path: (c) => `/clusters/${c}/resources`, label: '资源目录' },
  { key: 'observe', path: (c) => `/clusters/${c}/observe`, label: '观测中心' },
  { key: 'graph', path: (c) => `/clusters/${c}/graph`, label: '资源关系图谱' },
  { key: 'assistant', path: (c) => `/clusters/${c}/assistant`, label: '智能运维助手' },
  { key: 'investigations', path: (c) => `/clusters/${c}/investigations`, label: '调查中心' },
  { key: 'actions', path: (c) => `/clusters/${c}/actions`, label: '处置中心' },
  { key: 'knowledge', path: (c) => `/clusters/${c}/knowledge`, label: '运维知识' },
  { key: 'reports', path: (c) => `/clusters/${c}/reports`, label: '报告中心' },
  { key: 'admin', path: () => '/admin', label: '系统设置' },
]

// G10 fixed viewports. Every route at 1440; the listed subsets at 1280/1024.
const VIEWPORT_ROUTES = {
  '1440x900': ALL_ROUTES.map((route) => route.key),
  '1280x720': ['overview', 'cluster', 'resources', 'observe', 'graph', 'investigations', 'actions', 'assistant'],
  '1024x768': ['overview', 'cluster', 'resources', 'observe', 'graph', 'investigations', 'actions'],
  '1024x900': ['assistant', 'knowledge'],
}

function sh(command) {
  try {
    return execSync(command, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return ''
  }
}

function resolveBinding() {
  const commit = sh('git rev-parse HEAD')
  const short = commit ? commit.slice(0, 12) : ''
  const imageTag = process.env.AIOPS_RELEASE_IMAGE_TAG || (short ? `git-${short}` : '')
  let helmRevision = process.env.AIOPS_HELM_REVISION || ''
  let rollbackRevision = process.env.AIOPS_ROLLBACK_REVISION || ''
  if (!helmRevision) {
    const history = sh("helm history aiops -n observability -o json")
    if (history) {
      try {
        const rows = JSON.parse(history)
        const deployed = rows.filter((row) => row.status === 'deployed')
        if (deployed.length) helmRevision = String(deployed[deployed.length - 1].revision)
      } catch {}
    }
  }
  return { commit, imageTag, helmRevision, rollbackRevision }
}

// Scan the primary content area for cells that wrap character by character.
async function collectPrimaryCellMetrics(page) {
  return page.evaluate(() => {
    const selector = '.ant-table-cell, .platform-cluster-row strong, .causal-chain__source, .causal-chain__target, .key-evidence__item'
    const nodes = Array.from(document.querySelectorAll(selector)).slice(0, 400)
    return nodes.map((node) => {
      const text = (node.textContent || '').trim()
      const range = document.createRange()
      range.selectNodeContents(node)
      const rects = Array.from(range.getClientRects()).filter((rect) => rect.width > 1)
      return {
        textLength: text.length,
        renderedLines: Math.max(1, rects.length),
        clientWidth: Math.round(node.getBoundingClientRect().width),
        hasCopyOrDetailEntry: Boolean(node.closest('.ant-table-row')?.querySelector('.anticon-copy, [class*="copyable"]')),
        sample: text.slice(0, 24),
      }
    }).filter((metric) => metric.textLength > 8)
  })
}

async function collectViewport(page, route, tag, failures, screenshots) {
  const target = route.path(ENV.clusterId)
  await spaNav(page, target)
  await page.waitForTimeout(300)
  await waitForRouteSettled(page, target, failures)
  if (route.key === 'graph') await verifyGraphSemantics(page, target, tag, failures, screenshots)

  const bodyText = await page.locator('body').innerText().catch(() => '')
  try {
    assertNoForbiddenProductCopy(bodyText)
  } catch (error) {
    failures.push({ viewport: tag, route: target, check: 'forbidden_copy', detail: error.message })
  }

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  if (overflow > 2) failures.push({ viewport: tag, route: target, check: 'horizontal_overflow', detail: `${overflow}px` })

  try {
    assertReadablePrimaryCells(await collectPrimaryCellMetrics(page))
  } catch (error) {
    const metrics = (error.metrics || []).map((m) => `${m.sample}…(w=${m.clientWidth},lines=${m.renderedLines})`)
    failures.push({ viewport: tag, route: target, check: 'primary_cell_readable', detail: `${error.message}: ${metrics.join(' | ')}` })
  }

  const screenshot = `v3-${route.key}--${tag}.png`
  await shot(page, `v3-${route.key}--${tag}`)
  const screenshotPath = path.join(ROOT, 'screenshots', screenshot)
  if (!fs.existsSync(screenshotPath) || fs.statSync(screenshotPath).size === 0) {
    failures.push({ viewport: tag, route: target, check: 'screenshot_non_empty', detail: screenshot })
  } else {
    screenshots.push(`screenshots/${screenshot}`)
  }
  return { viewport: tag, route: target, title: route.label }
}

async function fetchJson(page, apiPath) {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'include' })
    if (!response.ok) return { __status: response.status }
    return { __status: response.status, body: await response.json() }
  }, apiPath)
}

// A screenshot is evidence only after the route has settled.  Skeletons and
// spinners are not a valid substitute for a real empty/error/partial state.
async function waitForRouteSettled(page, target, failures) {
  const deadline = Date.now() + 15000
  while (Date.now() < deadline) {
    const loading = await page.locator('.ant-skeleton, .ant-spin-spinning').count().catch(() => 0)
    const explicitState = await page.getByRole('status').filter({ hasText: /暂无|开始|不可用|失败|不完整|陈旧|部分/ }).count().catch(() => 0)
    if (loading === 0 || explicitState > 0) return { loading, explicitState }
    await page.waitForTimeout(250)
  }
  const remaining = await page.locator('.ant-skeleton, .ant-spin-spinning').count().catch(() => 0)
  if (remaining > 0) failures.push({ route: target, check: 'loading_timeout', detail: `${remaining} loading placeholders remained after 15s` })
  return { loading: remaining, explicitState: 0 }
}

async function verifyGraphSemantics(page, target, tag, failures, screenshots) {
  const query = (process.env.AIOPS_E2E_GRAPH_QUERY || '').trim()
  if (!query) {
    failures.push({ route: target, check: 'graph_query_configured', detail: 'AIOPS_E2E_GRAPH_QUERY is required for a real graph query' })
    return false
  }
  const input = page.getByLabel('资源关系搜索')
  if (await input.count() !== 1) {
    failures.push({ route: target, check: 'graph_search_input', detail: '资源关系搜索 is missing' })
    return false
  }
  await input.fill(query)
  await input.press('Enter')
  const candidates = page.locator('section[aria-label="资源关系探索"] button').filter({ hasText: query })
  try { await candidates.first().waitFor({ state: 'visible', timeoutMs: 12000 }) } catch {
    failures.push({ route: target, check: 'graph_query_results', detail: `no candidate resource for query=${query}` })
    return false
  }
  await candidates.first().click()
  const graph = page.locator('[aria-label="资源关系图"]')
  try { await graph.waitFor({ state: 'visible', timeoutMs: 15000 }) } catch {
    failures.push({ route: target, check: 'graph_rendered_for_query', detail: `query=${query}` })
    return false
  }
  const relationRegion = page.getByRole('region', { name: '关系明细' })
  const relationButtons = relationRegion.getByRole('button').filter({ hasText: /→/ })
  const relationCount = await relationButtons.count()
  const graphCanvasCount = await graph.locator('canvas').count()
  const legend = await page.getByText('箭头：源资源 → 目标资源', { exact: false }).count()
  const directionRows = await relationButtons.allTextContents({ timeoutMs: 3000 }).catch(() => [])
  const hasChineseRelation = directionRows.some((text) => /拥有|依赖|暴露|连接|挂载|运行于|传播/.test(text))
  if (graphCanvasCount < 1 || relationCount < 1 || legend < 1 || !hasChineseRelation) {
    failures.push({ route: target, check: 'graph_semantics', detail: JSON.stringify({ graphCanvasCount, relationCount, legend, directionRows: directionRows.slice(0, 3) }) })
    return false
  }
  const screenshot = `v3-graph-real--${tag}.png`
  await shot(page, `v3-graph-real--${tag}`)
  const screenshotPath = path.join(ROOT, 'screenshots', screenshot)
  if (fs.existsSync(screenshotPath) && fs.statSync(screenshotPath).size > 0) screenshots.push(`screenshots/${screenshot}`)
  return true
}

async function verifyClusterIsolation(page, clustersPayload, failures) {
  const rows = Array.isArray(clustersPayload?.clusters) ? clustersPayload.clusters : []
  const clusterIds = rows.map((row) => row.cluster_id).filter(Boolean)
  if (clusterIds.length < 2) {
    failures.push({ check: 'cluster_isolation', detail: `requires two authorized Kubernetes clusters; found=${clusterIds.length}` })
    return false
  }
  const snapshots = []
  for (const clusterId of clusterIds.slice(0, 2)) {
    const scope = await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: clusterId } })
    if (!scope.ok()) {
      failures.push({ check: 'cluster_isolation', detail: `scope switch failed cluster=${clusterId} status=${scope.status()}` })
      return false
    }
    const response = await page.request.get(`${ENV.apiBase}/resources/catalog?domain=kubernetes&type=k8s_service&q=kubernetes&limit=100`)
    if (!response.ok()) {
      failures.push({ check: 'cluster_isolation', detail: `catalog read failed cluster=${clusterId} status=${response.status()}` })
      return false
    }
    const body = await response.json().catch(() => ({}))
    const item = (body.items || []).find((candidate) => candidate.name === 'kubernetes' && candidate.namespace === 'default')
    if (!item || item.cluster_id !== clusterId || !item.uid) {
      failures.push({ check: 'cluster_isolation', detail: `same-name default/kubernetes Service missing or unscoped cluster=${clusterId}` })
      return false
    }
    snapshots.push(item)
  }
  await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId } })
  const distinctUid = snapshots[0].uid !== snapshots[1].uid
  const distinctCluster = snapshots[0].cluster_id !== snapshots[1].cluster_id
  if (!distinctUid || !distinctCluster) {
    failures.push({ check: 'cluster_isolation', detail: `same-name Service identity collision: ${JSON.stringify(snapshots)}` })
    return false
  }
  // An explicitly selected second cluster is valid because both clusters are
  // authorized.  Fail-closed is asserted with an unknown cluster identity,
  // which must never be treated as a real empty catalog.
  const invalidClusterId = '00000000-0000-0000-0000-000000000000'
  const cross = await page.request.get(`${ENV.apiBase}/resources/catalog?cluster_id=${invalidClusterId}&domain=kubernetes&type=k8s_service&q=kubernetes&limit=100`)
  if (cross.ok()) {
    failures.push({ check: 'cluster_isolation', detail: `invalid cluster_id unexpectedly returned 2xx (${invalidClusterId})` })
    return false
  }
  return true
}

async function verifyInvestigationTruth(page, failures) {
  const response = await page.request.get(`${ENV.apiBase}/ai/runs?cluster_id=${encodeURIComponent(ENV.clusterId)}&limit=100`)
  if (!response.ok()) {
    failures.push({ check: 'investigation_truth', detail: `runs list status=${response.status()}` })
    return 'FAIL'
  }
  const body = await response.json().catch(() => ({}))
  const runs = Array.isArray(body.runs) ? body.runs : []
  const terminals = runs.filter((run) => ['success', 'failed', 'cancelled', 'partial'].includes(run.status))
  if (terminals.length === 0) return 'BLOCKED_BY_ENV'
  for (const terminal of terminals) {
    const detailResponse = await page.request.get(`${ENV.apiBase}/ai/runs/${encodeURIComponent(terminal.run_id)}`)
    if (!detailResponse.ok()) {
      failures.push({ check: 'investigation_truth', detail: `terminal run detail status=${detailResponse.status()}` })
      return 'FAIL'
    }
    const detail = await detailResponse.json().catch(() => ({}))
    const run = detail.run || {}
    const summary = run.investigation_summary
    const hasSummary = summary && typeof summary === 'object' && typeof summary.termination_reason === 'string' && summary.termination_reason.length > 0
    const hasFrozenWindow = typeof run.time_range_start === 'string' && typeof run.time_range_end === 'string'
    if (hasSummary && hasFrozenWindow) return 'PASS'
  }
  failures.push({ check: 'investigation_truth', detail: `terminal runs=${terminals.length}; none has persisted summary and frozen window` })
  return 'BLOCKED_BY_ENV'
}

async function run() {
  const binding = resolveBinding()
  const failures = []
  const screenshots = []
  const visited = []
  const gates = {
    // Every gate starts unverified.  A gate may become PASS only after the
    // live assertion below succeeds; environment gaps are explicit blockers.
    cluster_isolation: 'FAIL',
    kubevirt_boundary: 'FAIL',
    health_truth: 'FAIL',
    graph_semantics: 'FAIL',
    alert_investigation: 'FAIL',
    investigation_truth: 'FAIL',
    action_safety: 'FAIL',
    responsive_ui: 'FAIL',
  }

  if (!binding.commit || binding.commit.length !== 40) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'git_commit', detail: 'a 40-char commit is required for version binding' })
  }
  if (!binding.imageTag) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'image_tag', detail: 'AIOPS_RELEASE_IMAGE_TAG or a derivable git-<sha> tag is required' })
  }
  if (!binding.helmRevision) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'helm_revision', detail: 'helm history for release aiops is unavailable' })
  }

  const col = makeCollector('v3-acceptance')
  for (const [tag, routeKeys] of Object.entries(VIEWPORT_ROUTES)) {
    const [width, height] = tag.split('x').map(Number)
    await withSession({ viewport: { width, height }, col }, async (page) => {
      for (const routeKey of routeKeys) {
        const route = ALL_ROUTES.find((item) => item.key === routeKey)
        visited.push(await collectViewport(page, route, tag, failures, screenshots))
      }

      // Version-bound API truth checks run once per session at full width.
      if (tag === '1440x900') {
        const overview = await fetchJson(page, '/api/v1/platform/overview')
        const clusters = await fetchJson(page, '/api/v1/platform/clusters')
        if (overview.__status !== 200 || clusters.__status !== 200) {
          failures.push({ check: 'platform_api', detail: `overview=${overview.__status} clusters=${clusters.__status}` })
        } else {
          try {
            assertNoSyntheticHealthScore(overview.body)
            const clusterRows = clusters.body.clusters || []
            const active = clusterRows.find((row) => row.cluster_id === ENV.clusterId)
            const detail = await fetchJson(page, `/api/v1/clusters/${encodeURIComponent(ENV.clusterId)}/overview`)
            if (!active || detail.__status !== 200) throw new Error(`active cluster detail unavailable status=${detail.__status}`)
            assertHealthFacts(
              { status: active.status, stale: active.stale, covered: active.covered },
              { status: detail.body.status, stale: detail.body.stale, covered: detail.body.coverage?.covered === detail.body.coverage?.expected },
            )
            gates.health_truth = 'PASS'
          } catch (error) {
            failures.push({ check: 'synthetic_health_score', detail: error.message })
          }
          if (await verifyClusterIsolation(page, clusters.body, failures)) gates.cluster_isolation = 'PASS'
        }

        const kubevirt = await fetchJson(page, '/api/v1/infrastructure/vms')
        if (kubevirt.__status !== 200) {
          failures.push({ check: 'kubevirt_api', detail: `status=${kubevirt.__status}` })
        } else {
          try {
            assertKubeVirtCapability(kubevirt.body)
            if (kubevirt.body.installed === false || kubevirt.body.kubevirt_not_installed === true) {
              gates.kubevirt_boundary = 'BLOCKED_BY_ENV'
              failures.push({ check: 'kubevirt_boundary', detail: 'no installed KubeVirt capability in the authorized cluster' })
            } else {
              gates.kubevirt_boundary = 'PASS'
            }
          } catch (error) {
            failures.push({ check: 'kubevirt_capability', detail: error.message })
          }
        }

        // G8: auto investigations must be real read-only system Runs.  Lack of
        // a system sample is a blocker, never an implicit PASS.
        const investigation = await fetchJson(page, `/api/v1/ai/runs?cluster_id=${ENV.clusterId}`)
        if (investigation.__status === 200) {
          const runs = investigation.body.runs || []
          const readOnlyAuto = runs.filter((run) => run.principal_type === 'system')
          if (readOnlyAuto.length === 0) {
            gates.action_safety = 'BLOCKED_BY_ENV'
            failures.push({ check: 'action_safety', detail: 'no real system auto_readonly Run sample is available' })
          } else if (readOnlyAuto.some((run) => run.action_mode !== 'read_only' || (run.actions || []).length > 0)) {
            failures.push({ check: 'auto_investigation_actions', detail: 'system run carries actions' })
          } else {
            gates.action_safety = 'PASS'
          }
        } else {
          failures.push({ check: 'action_safety', detail: `runs status=${investigation.__status}` })
        }

        const alerts = await fetchJson(page, `/api/v1/alerts/events?limit=200&cluster_id=${encodeURIComponent(ENV.clusterId)}`)
        if (alerts.__status !== 200) {
          failures.push({ check: 'alert_investigation', detail: `alerts status=${alerts.__status}` })
        } else {
          const events = Array.isArray(alerts.body?.events) ? alerts.body.events : (Array.isArray(alerts.body) ? alerts.body : [])
          await spaNav(page, `/clusters/${ENV.clusterId}/observe`)
          await waitForRouteSettled(page, `/clusters/${ENV.clusterId}/observe`, failures)
          const observeText = await page.locator('body').innerText().catch(() => '')
          const explicitEmpty = /暂无告警|暂无事件|没有告警/.test(observeText)
          const alertRows = await page.locator('[data-testid="alert-row"], [data-testid="notification-alert-item"], .alert-row').count().catch(() => 0)
          const investigationEntry = await page.getByText(/进入调查|发起调查|调查/).count().catch(() => 0)
          if ((events.length === 0 && alertRows > 0 && investigationEntry > 0) || (events.length > 0 && alertRows > 0 && investigationEntry > 0) || (events.length === 0 && explicitEmpty)) {
            gates.alert_investigation = 'PASS'
          } else {
            failures.push({ check: 'alert_investigation', detail: `events=${events.length} alertRows=${alertRows} explicitEmpty=${explicitEmpty} investigationEntry=${investigationEntry}` })
          }
        }

        const investigationTruth = await verifyInvestigationTruth(page, failures)
        gates.investigation_truth = investigationTruth

      }
    })
  }

  // One shared session collector covers every viewport.
  try {
    assertNoBadResponses(col.badResponses)
  } catch (error) {
    gates.health_truth = 'FAIL'
    gates.kubevirt_boundary = 'FAIL'
    failures.push({ check: 'bad_responses', detail: error.message })
  }
  if (col.pageErrors.length > 0) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'pageerror', detail: JSON.stringify(col.pageErrors.slice(0, 3)) })
  }
  if (visited.length !== Object.values(VIEWPORT_ROUTES).flat().length) {
    failures.push({ check: 'route_coverage', detail: `${visited.length} visited` })
  }

  const graphFailures = failures.filter((failure) => ['graph_query_configured', 'graph_search_input', 'graph_query_results', 'graph_rendered_for_query', 'graph_semantics'].includes(failure.check))
  if (graphFailures.length === 0) gates.graph_semantics = 'PASS'

  if (failures.filter((failure) => ['loading_timeout', 'horizontal_overflow', 'primary_cell_readable', 'forbidden_copy', 'pageerror', 'route_coverage'].includes(failure.check)).length === 0 && col.pageErrors.length === 0) {
    gates.responsive_ui = 'PASS'
  }
  for (const gate of Object.keys(gates)) {
    if (failures.some((failure) => failure.check === gate || failure.check.startsWith(`${gate}_`))) gates[gate] = gates[gate] === 'BLOCKED_BY_ENV' ? gates[gate] : 'FAIL'
  }
  const status = failures.length === 0 && Object.values(gates).every((value) => value === 'PASS') ? 'PASS' : 'FAIL'

  const doc = {
    schema_version: 2,
    status,
    generated_at: new Date().toISOString(),
    git_commit: binding.commit,
    image_tag: binding.imageTag,
    helm_revision: binding.helmRevision ? Number(binding.helmRevision) : null,
    rollback_revision: binding.rollbackRevision ? Number(binding.rollbackRevision) : null,
    gates,
    screenshots,
    failures,
    routes: visited,
    console_errors: col.consoleErrors.slice(0, 20),
  }

  fs.mkdirSync(ROOT, { recursive: true })
  fs.writeFileSync(path.join(ROOT, 'v3-acceptance.json'), JSON.stringify(doc, null, 2) + '\n')
  const summary = writeResult('V3-ACCEPTANCE', 'release', { checks: [{ name: 'v3_acceptance', pass: status === 'PASS' }], notes: failures.map((f) => `${f.check}: ${f.detail}`) })
  console.log(JSON.stringify({ status, output: path.join(ROOT, 'v3-acceptance.json'), failures: failures.length, summary }))
  if (status !== 'PASS') process.exitCode = 1
  return doc
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run, resolveBinding }
