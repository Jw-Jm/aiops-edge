const fs = require('fs')
const path = require('path')
const { MANIFEST } = require('./manifest')
const { classifyCase } = require('./result-policy')
const { summarizeItems, writeSummaryAtomic } = require('./summarize')
const { runPageData, requestJson, extractItems } = require('./page-data')
const { runBackend } = require('./backend')
const { ensureStrictChain } = require('../lib/strict-chain')
const { ENV, ROOT, withSession, makeCollector, spaNav } = require('../lib/laneD-runner')

function result(id, checks, ledger) {
  const telemetryReady = ledger?.telemetry_ready !== false
  const spec = MANIFEST.find((item) => item.id === id)
  return classifyCase({
    id, checks, spec,
    preflight: { telemetryReady, telemetryEvidence: ledger?.telemetry || { marker: ledger?.marker } },
  })
}

function check(name, pass, detail) { return { name, pass: Boolean(pass), detail: String(detail || '').slice(0, 600) } }

const STRICT_PAGE_ROUTES = [
  ['PF-PAGE-001', '/login'],
  ['PF-PAGE-002', '/change-password'],
  ['PF-PAGE-003', '/overview'],
  ['PF-PAGE-004', '/observability/service'],
  ['PF-PAGE-005', '/observability/relationships'],
  ['PF-PAGE-006', '/observability/trace'],
  ['PF-PAGE-007', '/observability/log'],
  ['PF-PAGE-008', '/observability/vms'],
  ['PF-PAGE-009', '/observability/grafana'],
  ['PF-PAGE-010', '/alerts/events'],
  ['PF-PAGE-011', '/alerts/rules'],
  ['PF-PAGE-012', '/ai/chat'],
  ['PF-PAGE-013', '/investigation'],
  ['PF-PAGE-014', '/investigation/new'],
  ['PF-PAGE-015', '/investigation/__RUN__'],
  ['PF-PAGE-016', '/investigation/__RUN__/evidence/__EVID__'],
  ['PF-PAGE-017', '/capacity'],
  ['PF-PAGE-018', '/infra/k8s'],
  ['PF-PAGE-019', '/hardware'],
  ['PF-PAGE-020', '/report'],
  ['PF-PAGE-021', '/changes'],
  ['PF-PAGE-022', '/admin/approvals'],
  ['PF-PAGE-023', '/admin/users'],
  ['PF-PAGE-024', '/admin/settings'],
  ['PF-PAGE-025', '/admin/graph-operations'],
  ['PF-PAGE-026', '/__e2e_not_found__'],
]

async function run() {
  const items = []
  const vp = ENV.viewports[0]
  const col = makeCollector(`${vp.width}x${vp.height}`)
  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    const ledger = await ensureStrictChain(page.request)
    const telemetryReady = ledger.telemetry_ready !== false
    const get = (route) => requestJson(page.request, ENV.apiBase, route)

    items.push(...await runPageData({ request: page.request, apiBase: ENV.apiBase, telemetryReady, telemetryEvidence: ledger.telemetry, marker: ledger.marker }))
    items.push(...await runBackend({ request: page.request, apiBase: ENV.apiBase, tenantId: ENV.tenantId, clusterId: ENV.clusterId, telemetryReady, telemetryEvidence: ledger.telemetry, marker: ledger.marker }))

    const clusters = await get('/clusters')
    const clusterRows = extractItems(clusters.body)
    const twoClusters = clusterRows.filter((row) => row.cluster_id && row.tenant_id === ENV.tenantId).length >= 2
    items.push(result('PF-LOGIC-002', [check('two_canonical_clusters', twoClusters, `clusters=${clusterRows.length}`)], ledger))
    items.push(result('PF-FLOW-007', [check('two_canonical_clusters', twoClusters, `clusters=${clusterRows.length}`)], ledger))

    const runId = ledger.artifacts?.run_readback?.body?.run_id || ledger.artifacts?.run?.body?.run_id || '__e2e_no_run__'
    const evidenceId = ledger.artifacts?.evidence?.body?.evidence_id || ledger.artifacts?.evidence?.body?.id || '__e2e_no_evidence__'
    for (const [id, routeTemplate] of STRICT_PAGE_ROUTES) {
      const route = routeTemplate.replace('__RUN__', runId).replace('__EVID__', evidenceId)
      await spaNav(page, route)
      await page.waitForTimeout(300)
      const bodyText = ((await page.locator('body').innerText().catch(() => '')) || '').trim()
      const rendered = bodyText.length > 20 || /暂无|加载中|not found|404|错误|失败|empty/i.test(bodyText)
      items.push(result(id, [check('page_rendered_or_explicit_empty', rendered, `${route} text=${bodyText.length}`)], ledger))
    }

    const uiRoutes = ['/overview', '/observability/trace', '/alerts/events', '/admin/settings', '/investigation', '/infra/k8s']
    for (const route of uiRoutes) {
      await spaNav(page, route)
      await page.waitForTimeout(500)
      const main = ((await page.locator('main').textContent().catch(() => '')) || '').trim()
      const uiPass = main.length > 100 || /暂无|加载中|empty/i.test(main)
      const id = route === '/overview' ? 'PF-UI-001' : route === '/observability/trace' ? 'PF-UI-002' : route === '/alerts/events' ? 'PF-UI-003' : route === '/admin/settings' ? 'PF-UI-004' : route === '/investigation' ? 'PF-UI-005' : 'PF-UI-006'
      items.push(result(id, [check('page_rendered_or_explicit_empty', uiPass, `${route} text=${main.length}`)], ledger))
    }

    const genericTelemetry = ['PF-LOGIC-003', 'PF-LOGIC-004', 'PF-LOGIC-012', 'PF-LOGIC-013', 'PF-LOGIC-014', 'PF-LOGIC-015', 'PF-FLOW-001', 'PF-FLOW-005', 'PF-FLOW-006']
    for (const id of genericTelemetry) {
      items.push(result(id, [check('strict_chain_marker', Boolean(ledger.marker), ledger.marker)], ledger))
    }
    const genericNonTelemetry = ['PF-LOGIC-001']
    for (const id of genericNonTelemetry) {
      const me = await get('/me')
      items.push(result(id, [check('authenticated_me_endpoint', me.status === 200 && me.body?.username === ENV.username, `me=${me.status}`)], ledger))
    }

    const requiredArtifacts = {
      'PF-LOGIC-005': ['chat_turn_1', 'chat_turn_2', 'chat_session_readback'],
      'PF-LOGIC-006': ['run', 'run_readback', 'tool_runs', 'evidence'],
      'PF-LOGIC-007': ['run_readback', 'graph_context'],
      'PF-LOGIC-008': ['action', 'action_readback'],
      'PF-LOGIC-009': ['action', 'action_readback'],
      'PF-LOGIC-011': ['report', 'knowledge', 'knowledge_readback'],
      'PF-AI-001': ['chat_turn_1', 'chat_turn_2', 'chat_session_readback'],
      'PF-AI-002': ['run', 'run_readback'], 'PF-AI-003': ['tool_runs', 'evidence'],
      'PF-AI-004': ['run_readback', 'graph_context'], 'PF-AI-005': ['run_readback', 'evidence'],
      'PF-AI-006': ['run_readback'], 'PF-AI-007': ['action', 'action_readback'],
      'PF-AI-008': ['action', 'action_readback'], 'PF-AI-009': ['action_readback'],
      'PF-AI-010': ['report', 'knowledge', 'knowledge_readback'],
      'PF-FLOW-002': ['chat_turn_1', 'chat_turn_2', 'run_readback'],
      'PF-FLOW-003': ['run_readback', 'tool_runs', 'evidence', 'graph_context'],
      'PF-FLOW-004': ['action', 'action_readback'], 'PF-FLOW-008': ['report', 'knowledge', 'knowledge_readback'],
    }
    for (const [id, names] of Object.entries(requiredArtifacts)) {
      const checks = names.map((name) => check(`artifact_${name}`, ledger.artifacts?.[name]?.status >= 200 && ledger.artifacts?.[name]?.status < 300, `${name}=${ledger.artifacts?.[name]?.status || 0}`))
      items.push(result(id, checks, ledger))
    }
  })

  const byId = new Map(items.map((item) => [item.id, item]))
  // Keep one result per manifest entry; missing IDs are explicit ERRORs so the
  // strict summary cannot silently pass an omitted runner.
  for (const spec of MANIFEST) {
    if (!byId.has(spec.id)) byId.set(spec.id, { id: spec.id, status: 'ERROR', failedChecks: ['runner_missing'], checks: [] })
  }
  const summary = summarizeItems({
    manifest: MANIFEST,
    items: MANIFEST.map((spec) => byId.get(spec.id)),
    metadata: { run_id: process.env.TEST_RUN_ID || 'strict-live', marker: process.env.STRICT_TELEMETRY_MARKER || '', generated_at: new Date().toISOString() },
  })
  writeSummaryAtomic(path.join(ROOT, 'strict', 'summary.json'), summary)
  console.log(JSON.stringify(summary.counts))
  if (!summary.strict_go) process.exitCode = 1
}

run().catch((error) => { console.error(error); process.exitCode = 1 })
