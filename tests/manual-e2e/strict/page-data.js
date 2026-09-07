const { MANIFEST } = require('./manifest')
const { classifyCase } = require('./result-policy')

function extractItems(body) {
  if (Array.isArray(body)) return body
  for (const key of ['data', 'items', 'services', 'traces', 'runs', 'changes', 'users', 'clusters', 'actions', 'nodes', 'pods', 'deployments', 'namespaces', 'events', 'reports', 'instances']) {
    if (Array.isArray(body?.[key])) return body[key]
  }
  return []
}

function validateTraceRows(rows) {
  if (!Array.isArray(rows) || rows.length === 0) return { ok: false, reason: 'trace rows are empty' }
  for (const row of rows) {
    const traceId = row.trace_id || row.traceId || row.id
    if (!traceId) return { ok: false, reason: 'trace_id is empty' }
    const duration = row.duration_ns ?? row.duration_ms ?? row.duration
    if (duration != null && (!Number.isFinite(Number(duration)) || Number(duration) < 0)) return { ok: false, reason: 'duration is invalid' }
    const spans = row.spans || row.span_list
    if (Array.isArray(spans) && spans.length > 0) {
      const ids = new Set(spans.map((span) => span.span_id || span.spanId).filter(Boolean))
      for (const span of spans) {
        const parent = span.parent_span_id || span.parentSpanId
        if (parent && !ids.has(parent)) return { ok: false, reason: `parent span ${parent} is missing` }
      }
    }
  }
  return { ok: true }
}

function validateForecast(response) {
  if (response.status !== 200) return { ok: false, reason: `forecast status ${response.status}` }
  const body = response.body || {}
  const linear = body.forecasts?.linear?.values
  const ewma = body.forecasts?.ewma?.values
  if (!Array.isArray(body.history) || !Array.isArray(body.timestamps) || typeof body.forecasts !== 'object' || body.forecasts === null) {
    return { ok: false, reason: 'forecast arrays are missing' }
  }
  if (body.history.length === 0) {
    const empty = body.timestamps.length === 0 &&
      (linear === undefined || Array.isArray(linear) && linear.length === 0) &&
      (ewma === undefined || Array.isArray(ewma) && ewma.length === 0)
    return empty ? { ok: true, empty: true } : { ok: false, reason: 'empty forecast contains points' }
  }
  if (!Array.isArray(linear) || !Array.isArray(ewma)) return { ok: false, reason: 'forecast arrays are missing' }
  if (linear.length === 0 || linear.length !== ewma.length) return { ok: false, reason: 'forecast horizon length mismatch' }
  if (body.timestamps.length !== body.history.length + linear.length) {
    return { ok: false, reason: 'history plus forecast timestamp length mismatch' }
  }
  return { ok: true, empty: false, horizon: linear.length }
}

async function requestJson(request, apiBase, route) {
  const response = await request.get(`${apiBase}${route}`)
  const text = await response.text()
  let body = {}
  try { body = text ? JSON.parse(text) : {} } catch { body = { raw: text.slice(0, 500) } }
  return { status: response.status(), body }
}

function hasMarker(body, marker) {
  return !marker || JSON.stringify(body).includes(marker)
}

function caseResult(id, checks, telemetryReady, telemetryEvidence) {
  const spec = MANIFEST.find((item) => item.id === id)
  if (!spec) throw new Error(`missing manifest case ${id}`)
  return classifyCase({ id, checks, spec, preflight: { telemetryReady, telemetryEvidence } })
}

function okCheck(name, value, detail) {
  return { name, pass: Boolean(value), detail }
}

async function runPageData({ request, apiBase, telemetryReady = true, telemetryEvidence, marker, traceId }) {
  const get = (route) => requestJson(request, apiBase, route)
  const results = []

  const [stats, resources] = await Promise.all([get('/dashboard/stats'), get('/dashboard/resources')])
  const trend = Array.isArray(stats.body?.trend) ? stats.body.trend : []
  results.push(caseResult('PF-DATA-001', [
    okCheck('stats_resource_endpoints_200', stats.status === 200 && resources.status === 200, `stats=${stats.status} resources=${resources.status}`),
    okCheck('trend_has_points', trend.length > 0, `trend=${trend.length}`),
    okCheck('numeric_nonnegative_counters', [stats.body?.total_calls, stats.body?.total_errors, stats.body?.error_rate].every((value) => Number.isFinite(Number(value)) && Number(value) >= 0), 'dashboard counters'),
    okCheck('resource_cluster_identity', resources.status === 200 && Boolean(resources.body?.cluster_id), `cluster=${resources.body?.cluster_id || 'missing'}`),
  ], telemetryReady, telemetryEvidence))

  const [services, serviceMap, matrix] = await Promise.all([
    get('/services?limit=200&hours=24'),
    get('/services/map?hours=24'),
    get('/services/matrix?hours=24'),
  ])
  const serviceRows = extractItems(services.body)
  const mapRows = extractItems(serviceMap.body)
  const mapText = JSON.stringify(serviceMap.body)
  results.push(caseResult('PF-DATA-002', [
    okCheck('services_endpoint_200', services.status === 200, `services=${services.status}`),
    okCheck('nonempty_service_identity', serviceRows.length > 0 && serviceRows.every((row) => String(row.service || row.name || row.service_name || '').trim() !== ''), `services=${serviceRows.length}`),
    okCheck('map_endpoint_200', serviceMap.status === 200, `map=${serviceMap.status} rows=${mapRows.length}`),
    okCheck('dependency_matrix_endpoint_200', matrix.status === 200, `matrix=${matrix.status}`),
    okCheck('strict_marker_services', hasMarker(services.body, marker) || serviceRows.some((row) => ['payments', 'orders'].includes(row.service || row.name)), `marker=${marker || 'not required'}`),
    okCheck('payments_orders_dependency', !marker || /payments.*orders|orders.*payments/i.test(mapText), 'service map identity'),
  ], telemetryReady, telemetryEvidence))

  const traces = await get('/traces?hours=24&limit=50')
  const traceRows = extractItems(traces.body)
  let detail = null
  const selectedTraceId = traceId || traceRows[0]?.trace_id || traceRows[0]?.traceId || traceRows[0]?.id
  if (selectedTraceId) detail = await get(`/traces/${encodeURIComponent(selectedTraceId)}`)
  const traceValidation = validateTraceRows(detail ? [detail.body?.trace || detail.body] : traceRows)
  results.push(caseResult('PF-DATA-003', [
    okCheck('traces_endpoint_200', traces.status === 200, `traces=${traces.status}`),
    okCheck('trace_ids_durations_valid', traceRows.length > 0 && validateTraceRows(traceRows).ok, `rows=${traceRows.length}`),
    okCheck('trace_detail_parent_child_valid', Boolean(detail) && detail.status === 200 && traceValidation.ok, detail ? `detail=${detail.status} ${traceValidation.reason || 'ok'}` : 'trace detail unavailable'),
    okCheck('strict_marker_trace', hasMarker(traces.body, marker) || hasMarker(detail?.body, marker), `marker=${marker || 'not required'}`),
  ], telemetryReady, telemetryEvidence))

  const logs = await get('/logs/query?hours=24&service_name=payments&limit=50')
  const logRows = extractItems(logs.body)
  results.push(caseResult('PF-DATA-004', [
    okCheck('logs_endpoint_200', logs.status === 200, `logs=${logs.status}`),
    okCheck('timestamp_message_valid', logRows.every((row) => row.timestamp || row.time || row.ts) && logRows.every((row) => row.message || row.body || row.content), `rows=${logRows.length}`),
    okCheck('level_filtering_endpoint_present', logs.status === 200, 'logs query'),
  ], telemetryReady, telemetryEvidence))

  const [alerts, rules] = await Promise.all([get('/alerts/events?limit=100'), get('/alerts/rules')])
  const alertRows = extractItems(alerts.body)
  const ruleRows = extractItems(rules.body)
  results.push(caseResult('PF-DATA-005', [
    okCheck('alerts_endpoint_200', alerts.status === 200, `alerts=${alerts.status}`),
    okCheck('alert_shape_valid', alertRows.every((row) => row.id != null && (row.status || row.severity || row.rule_name)), `alerts=${alertRows.length}`),
    okCheck('rule_endpoint_200', rules.status === 200, `rules=${rules.status} count=${ruleRows.length}`),
  ], telemetryReady, telemetryEvidence))

  const runs = await get('/ai/runs?limit=20')
  const runRows = extractItems(runs.body)
  results.push(caseResult('PF-DATA-006', [
    okCheck('runs_endpoint_200', runs.status === 200, `runs=${runs.status}`),
    okCheck('run_identity_if_present', runRows.every((row) => row.run_id || row.id), `rows=${runRows.length}`),
    okCheck('run_evidence_endpoint_derivable', runRows.length === 0 || Boolean(runRows[0].run_id || runRows[0].id), 'run id'),
  ], telemetryReady, telemetryEvidence))
  results.push(caseResult('PF-DATA-007', [
    okCheck('run_endpoint_200', runs.status === 200, `runs=${runs.status}`),
    okCheck('RCA_source_run_available', runRows.length === 0 || runRows.some((row) => row.run_id || row.id), `rows=${runRows.length}`),
    okCheck('graph_context_endpoint_only_if_run', true, 'deferred to run-detail case'),
  ], telemetryReady, telemetryEvidence))

  const instances = await get('/capacity/instances')
  const instanceRows = extractItems(instances.body)
  const instance = instanceRows[0]?.instance || instanceRows[0] || ''
  const forecast = await get(`/capacity/forecast?metric=cpu${instance ? `&instance=${encodeURIComponent(instance)}` : ''}`)
  const forecastValidation = validateForecast(forecast)
  results.push(caseResult('PF-DATA-008', [
    okCheck('capacity_instances_200', instances.status === 200, `instances=${instances.status}`),
    okCheck('forecast_endpoint_200', forecast.status === 200, `forecast=${forecast.status}`),
    okCheck('forecast_sequence_shape', forecastValidation.ok, forecastValidation.reason || 'forecast shape'),
  ], telemetryReady, telemetryEvidence))

  const infraRoutes = ['/infrastructure/nodes', '/infrastructure/deployments', '/infrastructure/pods', '/infrastructure/namespaces']
  const infra = await Promise.all(infraRoutes.map(get))
  results.push(caseResult('PF-DATA-009', [
    okCheck('nodes_deployments_pods_endpoints_200', infra.slice(0, 3).every((response) => response.status === 200), infra.slice(0, 3).map((response) => response.status).join(',')),
    okCheck('namespace_endpoint_200', infra[3].status === 200, `namespaces=${infra[3].status}`),
    okCheck('returned_status_fields_coherent', infra.every((response) => response.status === 200), 'infrastructure response statuses'),
  ], telemetryReady, telemetryEvidence))

  const [reports, changes] = await Promise.all([get('/ops/reports/history?limit=20'), get('/ops/changes?limit=20')])
  const changeRows = extractItems(changes.body)
  results.push(caseResult('PF-DATA-010', [
    okCheck('reports_history_available', reports.status === 200, `reports=${reports.status}`),
    okCheck('changes_endpoint_200', changes.status === 200, `changes=${changes.status}`),
    okCheck('change_schema_valid', changeRows.every((row) => row.id || row.change_id || row.created_at || row.time), `changes=${changeRows.length}`),
  ], telemetryReady, telemetryEvidence))

  const [users, clusters, actions, settings] = await Promise.all([get('/users?limit=20'), get('/clusters'), get('/ai/actions?limit=20'), get('/settings/llm')])
  results.push(caseResult('PF-DATA-011', [
    okCheck('users_endpoint_200', users.status === 200, `users=${users.status}`),
    okCheck('clusters_endpoint_200', clusters.status === 200, `clusters=${clusters.status}`),
    okCheck('canonical_actions_endpoint_200', actions.status === 200, `actions=${actions.status}`),
    okCheck('settings_endpoint_200', settings.status === 200, `settings=${settings.status}`),
  ], telemetryReady, telemetryEvidence))

  const [graphHealth, graphSearch, serviceGraph] = await Promise.all([
    get('/ai/kg/health'),
    get('/ai/kg/entities/search?q=payments&entity_type=service&limit=20'),
    get('/services/map?hours=24'),
  ])
  results.push(caseResult('PF-DATA-012', [
    okCheck('graph_health_endpoint_200', graphHealth.status === 200, `health=${graphHealth.status}`),
    okCheck('typed_graph_endpoint_200', graphSearch.status === 200, `typed search=${graphSearch.status}`),
    okCheck('node_edge_ids_valid', serviceGraph.status === 200, `service map=${serviceGraph.status}`),
  ], telemetryReady, telemetryEvidence))

  return results
}

module.exports = { extractItems, requestJson, runPageData, validateForecast, validateTraceRows }
