const { MANIFEST } = require('./manifest')
const { classifyCase } = require('./result-policy')
const { extractItems, hasStrictTraceEvidence, requestJson } = require('./page-data')

function isHttpSuccess(response) { return response && response.status >= 200 && response.status < 300 }

function shapeOf(body) {
  const keys = body && typeof body === 'object' && !Array.isArray(body) ? Object.keys(body).sort() : []
  return { keys, count: extractItems(body).length }
}

function sameIdentity(value, tenantId, clusterId) {
  return Boolean(value && value.tenant_id === tenantId && value.cluster_id === clusterId)
}

function caseResult(id, checks, telemetryReady, telemetryEvidence, facts = {}) {
  const spec = MANIFEST.find((item) => item.id === id)
  const result = classifyCase({ id, checks, spec, preflight: { telemetryReady, telemetryEvidence } })
  return { ...result, facts }
}

async function runBackend({ request, apiBase, tenantId, clusterId, telemetryReady = true, telemetryEvidence, marker, traceId }) {
  const get = (route) => requestJson(request, apiBase, route)
  const results = []
  const [me, runs, actions] = await Promise.all([get('/me'), get('/ai/runs?limit=20'), get('/ai/actions?limit=20')])
  results.push(caseResult('PF-BE-001', [
    { name: 'me_200', pass: isHttpSuccess(me), detail: `me=${me.status}` },
    { name: 'runs_endpoint_200', pass: isHttpSuccess(runs), detail: `runs=${runs.status}` },
    { name: 'canonical_actions_endpoint_200', pass: isHttpSuccess(actions), detail: `actions=${actions.status}` },
  ], telemetryReady, telemetryEvidence, { me: shapeOf(me.body), runs: shapeOf(runs.body), actions: shapeOf(actions.body) }))

  const [stats, resources] = await Promise.all([get('/dashboard/stats'), get('/dashboard/resources')])
  results.push(caseResult('PF-BE-002', [
    { name: 'dashboard_stats_200', pass: isHttpSuccess(stats), detail: `stats=${stats.status}` },
    { name: 'trend_numeric', pass: (stats.body?.trend || []).every((row) => Number.isFinite(Number(row.calls)) && Number.isFinite(Number(row.errors))), detail: `trend=${(stats.body?.trend || []).length}` },
    { name: 'resources_200', pass: isHttpSuccess(resources), detail: `resources=${resources.status}` },
  ], telemetryReady, telemetryEvidence, { stats: shapeOf(stats.body), resources: shapeOf(resources.body) }))

  const logs = await get('/logs/query?hours=24&limit=50')
  results.push(caseResult('PF-BE-003', [
    { name: 'logs_query_200', pass: isHttpSuccess(logs), detail: `logs=${logs.status}` },
    { name: 'logs_envelope', pass: Array.isArray(extractItems(logs.body)), detail: `rows=${extractItems(logs.body).length}` },
  ], telemetryReady, telemetryEvidence, { logs: shapeOf(logs.body) }))

  const traces = await get(`/traces?hours=24&limit=50${traceId ? `&trace_id=${encodeURIComponent(traceId)}` : ''}`)
  const traceRows = extractItems(traces.body)
  results.push(caseResult('PF-BE-004', [
    { name: 'traces_200', pass: isHttpSuccess(traces), detail: `traces=${traces.status}` },
    { name: 'trace_envelope', pass: Array.isArray(traceRows), detail: `rows=${traceRows.length}` },
    { name: 'trace_marker_if_required', pass: hasStrictTraceEvidence(traces.body, marker), detail: `marker=${marker || 'not required'}` },
    { name: 'trace_identifiers_if_rows', pass: traceRows.every((row) => row.trace_id || row.traceId || row.id), detail: `rows=${traceRows.length}` },
  ], telemetryReady, telemetryEvidence, { traces: shapeOf(traces.body) }))

  const [health, graphSearch, serviceMap] = await Promise.all([get('/ai/kg/health'), get('/ai/kg/entities/search?q=payments&entity_type=service&limit=20'), get('/services/map?hours=24')])
  results.push(caseResult('PF-BE-005', [
    { name: 'graph_health_200', pass: isHttpSuccess(health), detail: `health=${health.status}` },
    { name: 'typed_graph_endpoint_responded', pass: isHttpSuccess(graphSearch), detail: `typed search=${graphSearch.status}` },
    { name: 'service_map_endpoint_200', pass: isHttpSuccess(serviceMap), detail: `service map=${serviceMap.status}` },
  ], telemetryReady, telemetryEvidence, { health: shapeOf(health.body), graph: shapeOf(graphSearch.body), serviceMap: shapeOf(serviceMap.body) }))

  const [knowledge, rag] = await Promise.all([get('/ai/knowledge?limit=20'), get('/ai/knowledge/rag/stats')])
  results.push(caseResult('PF-BE-006', [
    { name: 'knowledge_list_200', pass: isHttpSuccess(knowledge), detail: `knowledge=${knowledge.status}` },
    { name: 'rag_stats_200', pass: isHttpSuccess(rag), detail: `rag=${rag.status}` },
  ], telemetryReady, telemetryEvidence, { knowledge: shapeOf(knowledge.body), rag: shapeOf(rag.body) }))

  const infraRoutes = ['/infrastructure/nodes', '/infrastructure/namespaces', '/infrastructure/pods', '/infrastructure/deployments']
  const infra = await Promise.all(infraRoutes.map(get))
  results.push(caseResult('PF-BE-007', [
    { name: 'nodes_namespaces_pods_deployments_200', pass: infra.every(isHttpSuccess), detail: infra.map((response) => response.status).join(',') },
    { name: 'node_fields_valid', pass: extractItems(infra[0].body).every((row) => row.name || row.node_name || row.metadata?.name), detail: `nodes=${extractItems(infra[0].body).length}` },
  ], telemetryReady, telemetryEvidence, Object.fromEntries(infraRoutes.map((route, index) => [route, shapeOf(infra[index].body)]))))

  const [rules, events] = await Promise.all([get('/alerts/rules'), get('/alerts/events?limit=100')])
  results.push(caseResult('PF-BE-008', [
    { name: 'alert_rules_200', pass: isHttpSuccess(rules), detail: `rules=${rules.status}` },
    { name: 'alert_events_200', pass: isHttpSuccess(events), detail: `events=${events.status}` },
    { name: 'rules_shape', pass: extractItems(rules.body).every((row) => row.id != null || row.name), detail: `rules=${extractItems(rules.body).length}` },
  ], telemetryReady, telemetryEvidence, { rules: shapeOf(rules.body), events: shapeOf(events.body) }))

  const [reports, changes] = await Promise.all([get('/ops/reports/history?limit=20'), get('/ops/changes?limit=20')])
  results.push(caseResult('PF-BE-009', [
    { name: 'reports_history_api', pass: isHttpSuccess(reports), detail: `reports=${reports.status}` },
    { name: 'changes_200', pass: isHttpSuccess(changes), detail: `changes=${changes.status}` },
    { name: 'change_fields', pass: extractItems(changes.body).every((row) => row.id || row.change_id || row.created_at || row.time), detail: `changes=${extractItems(changes.body).length}` },
  ], telemetryReady, telemetryEvidence, { reports: shapeOf(reports.body), changes: shapeOf(changes.body) }))

  const [clusters, scopedMe] = await Promise.all([get('/clusters'), get('/me')])
  const clusterRows = extractItems(clusters.body)
  results.push(caseResult('PF-BE-010', [
    { name: 'clusters_200', pass: isHttpSuccess(clusters), detail: `clusters=${clusters.status}` },
    { name: 'active_scope_identity', pass: scopedMe.body?.active_scope?.tenant_id === tenantId && scopedMe.body?.active_scope?.cluster_id === clusterId, detail: JSON.stringify(scopedMe.body?.active_scope || {}) },
    { name: 'cluster_identity_fields', pass: clusterRows.every((row) => row.cluster_id && row.tenant_id === tenantId), detail: `clusters=${clusterRows.length}` },
    { name: 'second_canonical_cluster_registered', pass: clusterRows.filter((row) => row.cluster_id && row.cluster_id !== clusterId).length >= 1, detail: `canonical_clusters=${clusterRows.map((row) => row.cluster_id).filter(Boolean).join(',')}` },
  ], telemetryReady, telemetryEvidence, { clusters: shapeOf(clusters.body), me: shapeOf(scopedMe.body) }))

  return results
}

module.exports = { isHttpSuccess, runBackend, sameIdentity, shapeOf }
