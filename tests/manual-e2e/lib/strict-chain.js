const fs = require('fs')
const path = require('path')
const crypto = require('crypto')
const { ENV, ROOT } = require('./laneD-runner')

function requestBody(response) {
  return response.text().then((text) => {
    try { return text ? JSON.parse(text) : {} } catch { return { raw: text.slice(0, 1000) } }
  })
}

function artifactFile() {
  const runId = process.env.TEST_RUN_ID || 'strict-live'
  const dir = path.join(ROOT, 'ai-chain')
  fs.mkdirSync(dir, { recursive: true })
  return path.join(dir, 'strict-chain.json')
}

function readLedger() {
  try { return JSON.parse(fs.readFileSync(artifactFile(), 'utf8')) } catch { return null }
}

function writeLedger(ledger) {
  const file = artifactFile()
  const temp = `${file}.tmp-${process.pid}-${Date.now()}`
  fs.writeFileSync(temp, `${JSON.stringify(ledger, null, 2)}\n`)
  fs.renameSync(temp, file)
}

function idempotency(prefix) {
  return `${prefix}-${process.env.TEST_RUN_ID || Date.now()}-${crypto.randomUUID()}`
}

async function getJson(request, route) {
  const response = await request.get(`${ENV.apiBase}${route}`)
  return { status: response.status(), headers: response.headers(), body: await requestBody(response) }
}

async function postJson(request, route, body) {
  const response = await request.post(`${ENV.apiBase}${route}`, { data: body })
  return { status: response.status(), headers: response.headers(), body: await requestBody(response) }
}

function ok(value) { return value && value.status >= 200 && value.status < 300 }

function evaluateTelemetryReadiness({ marker, stats, traces, logs, services, serviceMap }) {
  const expectedTraceID = crypto.createHash('sha256').update(`${marker}/1`).digest('hex')
  const statsText = JSON.stringify(stats?.body || {})
  const tracesText = JSON.stringify(traces?.body || {})
  const logsText = JSON.stringify(logs?.body || {})
  const servicesText = JSON.stringify(services?.body || {})
  const mapText = JSON.stringify(serviceMap?.body || {})
  const statusesOK = [stats, traces, logs, services, serviceMap].every((response) => response?.status === 200)
  const trendReady = Array.isArray(stats?.body?.trend) && stats.body.trend.length > 0
  const servicesReady = servicesText.includes('payments') && servicesText.includes('orders')
  const traceReady = tracesText.includes(expectedTraceID)
  const logsReady = logsText.includes(marker)
  const topologyReady = /payments.*orders|orders.*payments/i.test(mapText)
  return {
    ready: statusesOK && trendReady && servicesReady && traceReady && logsReady && topologyReady,
    marker_found: traceReady && logsReady,
    expected_trace_id: expectedTraceID,
    stats_status: stats?.status || 0,
    trace_status: traces?.status || 0,
    log_status: logs?.status || 0,
    service_status: services?.status || 0,
    map_status: serviceMap?.status || 0,
    trend_ready: trendReady,
    services_ready: servicesReady,
    trace_ready: traceReady,
    logs_ready: logsReady,
    topology_ready: topologyReady,
    checked_at: new Date().toISOString(),
  }
}

async function waitForRun(request, runId) {
  const timeout = Number(process.env.STRICT_CHAIN_TIMEOUT_MS || 120000)
  const deadline = Date.now() + timeout
  let latest = null
  while (Date.now() < deadline) {
    latest = await getJson(request, `/ai/runs/${encodeURIComponent(runId)}`)
    const run = latest.body?.run || latest.body
    if (run && ['completed', 'failed', 'cancelled', 'awaiting_approval', 'executing', 'verifying'].includes(run.status)) {
      if (['completed', 'failed', 'cancelled', 'awaiting_approval'].includes(run.status)) return latest
    }
    await new Promise((resolve) => setTimeout(resolve, 2000))
  }
  return latest || { status: 0, body: { error: 'run polling timeout' } }
}

async function ensureStrictChain(request) {
  const currentRunId = process.env.TEST_RUN_ID || `strict-${Date.now()}`
  const marker = process.env.STRICT_TELEMETRY_MARKER || `strict-${currentRunId}`
  const cached = readLedger()
  if (cached && cached.marker === marker && cached.checked_at && cached.telemetry_ready !== false) return cached

  const ledger = {
    run_id: currentRunId,
    marker,
    tenant_id: ENV.tenantId,
    primary_cluster_id: ENV.clusterId,
    checked_at: new Date().toISOString(),
    artifacts: {},
    checks: [],
  }
  const record = (name, response, identity = {}) => {
    const body = response?.body || {}
    const id = body.run_id || body.action_id || body.case_id || body.report_id || body.id
    ledger.artifacts[name] = {
      status: response?.status || 0,
      id,
      run_id: body.run_id,
      action_id: body.action_id,
      case_id: body.case_id,
      report_id: body.report_id,
      session_id: body.session_id || body.thread_id,
      ...identity,
    }
    ledger.checks.push({ name, pass: ok(response), status: response?.status || 0, id })
    return response
  }

  const [stats, traces, logs, services, serviceMap] = await Promise.all([
    getJson(request, '/dashboard/stats'),
    getJson(request, '/traces?service=payments&hours=24&limit=100'),
    getJson(request, '/logs/query?service_name=payments&hours=24&limit=500'),
    getJson(request, '/services?hours=24&limit=200'),
    getJson(request, '/services/map?hours=24'),
  ])
  ledger.telemetry = evaluateTelemetryReadiness({ marker: ledger.marker, stats, traces, logs, services, serviceMap })
  ledger.telemetry_ready = ledger.telemetry.ready
  if (!ledger.telemetry_ready) {
    ledger.checks.push({ name: 'fresh_telemetry', pass: false, status: 0 })
    writeLedger(ledger)
    return ledger
  }

  // Two-turn real Chat. The response is read back via the session API, not
  // treated as proof merely because the POST returned 200.
  let sessionId = ''
  const turnOne = await postJson(request, '/ai/chat', {
    intent: 'diagnosis', message: `Investigate ${ledger.marker}: payments to orders latency and errors`,
    stream: false, session_id: '', turn_id: crypto.randomUUID(), cluster_id: ENV.clusterId,
  })
  record('chat_turn_1', turnOne)
  sessionId = turnOne.body?.session_id || turnOne.body?.thread_id || turnOne.body?.session?.id || turnOne.headers?.['x-session-id'] || ''
  if (sessionId) {
    const turnTwo = await postJson(request, '/ai/chat', {
      intent: 'diagnosis', message: 'Use trace, logs, metrics, Kubernetes and graph evidence; state uncertainty explicitly.',
      stream: false, session_id: sessionId, turn_id: crypto.randomUUID(), cluster_id: ENV.clusterId,
    })
    record('chat_turn_2', turnTwo, { session_id: sessionId })
    const session = await getJson(request, `/ai/session/${encodeURIComponent(sessionId)}`)
    record('chat_session_readback', session, { session_id: sessionId })
    ledger.artifacts.chat_session = { session_id: sessionId, message_count: (session.body?.messages || []).length }
  }

  // One durable Run created through the public manual boundary.
  const runCreate = await postJson(request, '/ai/runs', {
    tenant_id: ENV.tenantId, cluster_id: ENV.clusterId, idempotency_key: idempotency('run'),
    intent: `Investigate ${ledger.marker} payments/orders`, action_mode: 'read_only',
    service: 'payments', resource_id: 'payments', target_type: 'service', message: ledger.marker,
  })
  record('run', runCreate, { cluster_id: ENV.clusterId })
  const runId = runCreate.body?.run_id
  if (runId) {
    const run = await waitForRun(request, runId)
    record('run_readback', run, { run_id: runId })
    const [tools, evidences, graph] = await Promise.all([
      getJson(request, `/ai/runs/${encodeURIComponent(runId)}/tools`),
      getJson(request, `/ai/runs/${encodeURIComponent(runId)}/evidences`),
      getJson(request, `/ai/runs/${encodeURIComponent(runId)}/graph-context`),
    ])
    record('tool_runs', tools, { run_id: runId })
    record('evidence', evidences, { run_id: runId })
    record('graph_context', graph, { run_id: runId })
  }

  // Proposal is real but bounded to the dedicated test namespace. Execution
  // and restoration are separate artifacts and are only attempted by the
  // action-specific manual case after independent approval.
  const action = await postJson(request, '/ai/actions', {
    idempotency_key: idempotency('action'), cluster_id: ENV.clusterId,
    resource_type: 'deployment', namespace: 'aiops-action-test', target_name: 'aiops-action-test',
    operation: 'rollout_restart', params: {},
  })
  record('action', action, { cluster_id: ENV.clusterId })
  if (action.body?.action_id) {
    const actionRead = await getJson(request, `/ai/actions/${encodeURIComponent(action.body.action_id)}`)
    record('action_readback', actionRead, { action_id: action.body.action_id })
  }

  // Report and knowledge use the same real Chat session/run identifiers.
  const report = sessionId
    ? await postJson(request, '/ai/final_report', { session_id: sessionId, service: 'payments' })
    : { status: 422, body: { error: 'chat session missing' } }
  record('report', report, { session_id: sessionId })
  const reportId = report.body?.report_id || report.body?.id
  const knowledge = await postJson(request, '/ai/knowledge/case', reportId ? { report_id: reportId } : {
    service: 'payments', symptom: ledger.marker, root_cause: report.body?.report || '', plan: 'review evidence',
  })
  record('knowledge', knowledge, { report_id: reportId })
  if (knowledge.body?.case_id || knowledge.body?.id) {
    const knowledgeRead = await getJson(request, '/ai/knowledge?limit=100')
    record('knowledge_readback', knowledgeRead, { case_id: knowledge.body.case_id || knowledge.body.id })
  }
  ledger.checked_at = new Date().toISOString()
  writeLedger(ledger)
  return ledger
}

function chainEvidence(ledger, artifactNames) {
  return artifactNames.every((name) => ledger?.artifacts?.[name]?.status >= 200 && ledger?.artifacts?.[name]?.status < 300)
}

module.exports = { ensureStrictChain, chainEvidence, evaluateTelemetryReadiness, getJson, postJson, readLedger, writeLedger }
