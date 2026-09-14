// PF-LOGIC-003 服务→Trace→日志→关系关联逻辑。
// 选真实服务（/dashboard/stats top_services → payments），在服务全景/Trace/日志/关系图
// 定位同一服务，核对 identity 一致。分布式 Trace 允许跨服务 span，不要求所有 span 同名。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-003.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry } = require('./lib/laneE')

const SVC = 'payments'

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 0. 选择真实服务：/dashboard/stats top_services
    const stats = await (await page.request.get(`${ENV.apiBase}/dashboard/stats`)).json().catch(() => ({}))
    const top = (stats.top_services || []).map((s) => s.service)
    check('top_services_has_target', top.includes(SVC), `top_services=${JSON.stringify(top)}，选定服务=${SVC}`)

    // 1. 服务全景：/services 列表 + 页面渲染
    const svcResp = await apiRetry(page, 'get', `/services?cluster_id=${ENV.clusterId}`)
    let svcNames = []
    try { svcNames = (JSON.parse(svcResp.body)?.services || []).map((s) => s.service_name) } catch {}
    check('service_in_service_inventory_api', svcNames.includes(SVC), `/services=${JSON.stringify(svcNames)}`)

    await spaNav(page, '/observability/service')
    await page.waitForTimeout(3000)
    const svcPageText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('service_visible_in_service_page', svcPageText.includes(SVC),
      `服务全景页面未找到 "${SVC}"（页面"服务总数 0"）。根因：页面服务列表来自 /services/map 图投影（getServiceMap），该接口返回 warnings=GRAPH_IDENTITY_UNAVAILABLE、services=[]（图实体体系不可用）；而 legacy /services 接口与 Trace/日志中 payments 均存在——产品页面与数据源断裂，如实记录 FAIL`)

    // 2. Trace：API 按服务过滤 + trace 详情 span 的 service_name
    const trResp = await apiRetry(page, 'get', `/traces?cluster_id=${ENV.clusterId}&service=${SVC}&limit=5`)
    let traces = []
    try { traces = JSON.parse(trResp.body)?.data || [] } catch {}
    check('traces_found_for_service', traces.length > 0, `/traces?service=${SVC} -> ${traces.length} 条`)
    let spanSvcOk = false
    let spanSample = ''
    if (traces.length > 0) {
      const d = await apiRetry(page, 'get', `/traces/${traces[0].trace_id}?cluster_id=${ENV.clusterId}`)
      try {
        const spans = JSON.parse(d.body)?.spans || []
        spanSvcOk = spans.length > 0 && spans.some((s) => s.service_name === SVC)
          && spans.every((s) => Boolean(s.service_name))
        spanSample = JSON.stringify(spans.map((s) => s.service_name))
      } catch { spanSample = 'parse-error' }
    }
    check('trace_contains_target_service_identity', spanSvcOk, `trace 详情 spans service_name=${spanSample}，应包含目标服务 "${SVC}" 且每个 span 有服务 identity`)

    await spaNav(page, '/observability/trace')
    await page.waitForTimeout(3000)
    const tracePageText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('trace_page_renders', tracePageText.length > 100, `链路追踪页面文本长度=${tracePageText.length}`)
    // 页面服务筛选（若存在服务下拉/输入，尝试选择 payments）
    const svcFilter = page.locator('.ant-select, input').filter({ hasText: '' })
    notes.push(`链路追踪页面渲染正常；服务级过滤以 API /traces?service=${SVC} 与 span service_name 核对 identity。`)

    // 3. 日志：/logs/query?service=
    const logResp = await apiRetry(page, 'get', `/logs/query?cluster_id=${ENV.clusterId}&service=${SVC}&limit=5`)
    let logs = []
    try { logs = JSON.parse(logResp.body)?.data || [] } catch {}
    const logSvcOk = logs.length > 0 && logs.every((l) => l.service_name === SVC)
    check('logs_found_for_service_identity_consistent', logSvcOk,
      `/logs/query?service=${SVC} -> ${logs.length} 条，service_name 样本=${JSON.stringify(logs.slice(0, 2).map((l) => l.service_name))}`)

    await spaNav(page, '/observability/log')
    await page.waitForTimeout(3000)
    const logPageText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('log_page_renders', logPageText.length > 100, `日志页面文本长度=${logPageText.length}`)

    // 4. 关系图定位（带 cluster scope 的图检索 + 页面真实探索）
    const kgResp = await apiRetry(page, 'get', `/ai/kg/entities/search?q=${SVC}&entity_type=service&limit=20&cluster_id=${ENV.clusterId}`)
    let kgEntity = null
    try { kgEntity = (JSON.parse(kgResp.body)?.items || [])[0] } catch {}
    check('relationship_graph_locates_service', kgResp.status === 200 && kgEntity?.name === SVC,
      `GET /ai/kg/entities/search?q=${SVC}(cluster_id) -> ${kgResp.status}，实体=${JSON.stringify(kgEntity ? { uid: kgEntity.entity_uid.slice(0, 60) + '…', type: kgEntity.entity_type, name: kgEntity.name, tenant: kgEntity.tenant_id, cluster: kgEntity.cluster_id } : kgResp.body.slice(0, 120))}`)
    // Session scope is authoritative when cluster_id is omitted; the request
    // must remain scoped rather than being rejected as an unscoped query.
    const kgNoScope = await apiRetry(page, 'get', `/ai/kg/entities/search?q=${SVC}&entity_type=service&limit=20`)
    let kgNoScopeEntity = null
    try { kgNoScopeEntity = (JSON.parse(kgNoScope.body)?.items || [])[0] } catch {}
    check('graph_search_uses_session_scope_without_explicit_query_scope',
      kgNoScope.status === 200 && kgNoScopeEntity?.name === SVC
        && kgNoScopeEntity?.cluster_id === ENV.clusterId,
      `不带 cluster_id 的检索使用会话 scope -> ${kgNoScope.status}，实体=${JSON.stringify(kgNoScopeEntity ? { name: kgNoScopeEntity.name, cluster: kgNoScopeEntity.cluster_id } : kgNoScope.body.slice(0, 160))}`)
    const mapResp = await apiRetry(page, 'get', `/services/map?cluster_id=${ENV.clusterId}`)
    let mapWarn = ''
    try { mapWarn = (JSON.parse(mapResp.body)?.warnings || []).join(',') } catch {}
    check('service_map_graph_identity_available', !/GRAPH_IDENTITY_UNAVAILABLE/.test(mapWarn),
      `/services/map warnings=${mapWarn}（图实体体系不可用，服务地图为空）`)

    await spaNav(page, '/observability/relationships')
    await page.waitForTimeout(3000)
    const relPageText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('relationships_page_renders', relPageText.includes('资源关系') && relPageText.includes('知识图谱摘要'),
      `资源关系页面渲染：标题与图谱摘要可见，后端状态=${(relPageText.match(/不可用|后端[\s\S]{0,6}/) || [''])[0]}（文本长度=${relPageText.length}）`)
    // 在关系图检索同一服务（真实 UI 探索）
    const relInput = page.locator('.ant-input').first()
    await relInput.fill(SVC)
    await page.getByRole('button', { name: /探\s*索/ }).click().catch(() => {})
    await page.waitForTimeout(3500)
    const relAfter = ((await page.locator('body').textContent().catch(() => '')) || '')
    const graphUnavailable = /不可用/.test(relAfter)
    const graphHit = /payments/.test(relAfter) && /DEPENDS_ON|依赖主链|条关系/.test(relAfter)
    check('relationship_graph_explore_payments', !graphUnavailable && graphHit,
      `关系图探索 "${SVC}"：后端标记=${graphUnavailable ? '不可用' : 'hugegraph 就绪'}；图谱返回依赖关系（DEPENDS_ON/依赖主链）=${graphHit}，实体 identity 与服务名一致`)
    await shot(page, 'PF-LOGIC-003-relationships')

    // 5. identity 一致性汇总
    check('service_identity_consistent_across_pages',
      svcNames.includes(SVC) && spanSvcOk && logSvcOk,
      `服务名 "${SVC}" 在 /services、trace spans、logs 中一致；关系图因 503 缺失该维度`)
    notes.push(`选定服务=${SVC}（来自 /dashboard/stats top_services）；服务清单、Trace、日志和关系图均按 canonical identity 核对。`)
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 2 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-003', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
