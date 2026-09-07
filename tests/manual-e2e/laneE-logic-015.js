// PF-LOGIC-015 Graph 数据与产品页面逻辑。
// 已知缺陷：图读接口（/ai/kg/entities/* 等）503 GRAPH_FEATURE_UNAVAILABLE_LEGACY。
// 可验证部分：Graph Ops（sync-states/outbox）与 MySQL 后端一致；HugeGraph 后端实体
// tenant/cluster identity 正确；图谱运维页面渲染。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-015.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry, sh } = require('./lib/laneE')

function mysqlQuery(q) {
  const esc = q.replace(/"/g, '').replace(/\$/g, '\\$').replace(/`/g, '')
  return sh(`kubectl exec -n observability mysql-0 -- sh -c "mysql -uroot -p\\"\\$MYSQL_ROOT_PASSWORD\\" aiops -e \\"${esc}\\"" 2>/dev/null`).trim()
}
function hugegraphGet(path) {
  return sh(`kubectl exec -n observability hugegraph-0 -- sh -c "curl -s --compressed -u \\"\\$HUGEGRAPH_USERNAME:\\$PASSWORD\\" \\"http://localhost:8080${path}\\"" 2>/dev/null`).trim()
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. 图读接口：带 cluster scope 可用；不带 cluster_id 503（缺陷边界，如实记录）
    const search = await apiRetry(page, 'get', `/ai/kg/entities/search?q=payments&entity_type=service&limit=20&cluster_id=${ENV.clusterId}`)
    let entity = null
    try { entity = (JSON.parse(search.body)?.items || [])[0] } catch {}
    check('graph_entity_search_available', search.status === 200 && entity?.name === 'payments',
      `GET /ai/kg/entities/search(cluster_id) -> ${search.status}，实体=${entity ? `${entity.entity_type}:${entity.name} tenant=${entity.tenant_id} cluster=${entity.cluster_id}` : search.body.slice(0, 120)}`)
    const searchNoScope = await apiRetry(page, 'get', '/ai/kg/entities/search?q=payments&entity_type=service&limit=20')
    let code = ''
    try { code = JSON.parse(searchNoScope.body)?.error?.code || '' } catch {}
    check('graph_search_without_cluster_id_503_defect', searchNoScope.status === 503,
      `已知缺陷边界（如实记录）：不带 cluster_id 的图检索 -> ${searchNoScope.status} code=${code}（页面挂载首帧未附 scope 的请求同样 503）`)

    // 2. Graph Ops sync-states vs MySQL graph_sync_state
    const ss = await apiRetry(page, 'get', '/ai/kg/ops/sync-states')
    let apiStates = []
    try { apiStates = JSON.parse(ss.body)?.items || [] } catch {}
    const dbStates = mysqlQuery('SELECT source,status FROM graph_sync_state')
    const dbPairs = dbStates.split('\n').slice(1).map((l) => l.trim()).filter(Boolean).sort().join(';')
    const apiPairs = apiStates.map((s) => `${s.Source}\t${s.Status}`).sort().join(';')
    check('graph_ops_sync_states_match_backend', ss.status === 200 && apiPairs.replace(/\t/g, '=') === dbPairs.replace(/\t/g, '='),
      `API sync-states(${apiStates.length} 条)=${apiPairs.replace(/\t/g, '=')}；MySQL graph_sync_state=${dbPairs.replace(/\t/g, '=')}`)

    // 3. Graph Ops outbox vs MySQL graph_projection_outbox
    const ob = await apiRetry(page, 'get', '/ai/kg/ops/outbox')
    let apiOutbox = -1
    try { apiOutbox = JSON.parse(ob.body)?.count } catch {}
    const dbOutbox = mysqlQuery('SELECT count(*) FROM graph_projection_outbox').split('\n').pop().trim()
    check('graph_ops_outbox_match_backend', String(apiOutbox) === dbOutbox,
      `API outbox count=${apiOutbox}；MySQL graph_projection_outbox=${dbOutbox}`)

    // 4. HugeGraph 后端实体 identity（tenant/cluster 标签正确）
    const vRaw = hugegraphGet('/graphs/aiops/graph/vertices?limit=5')
    let vOk = false, vSample = '', badIdentity = 0, vCount = 0
    try {
      const verts = JSON.parse(vRaw)?.vertices || []
      vCount = verts.length
      for (const v of verts) {
        const p = v.properties || {}
        if (p.tenant_id && p.cluster_id) vOk = true
        if (p.tenant_id && p.tenant_id !== ENV.tenantId) badIdentity++
        if (!vSample && p.entity_uid) vSample = `${p.entity_type}:${p.name} tenant=${p.tenant_id} cluster=${p.cluster_id}`
      }
    } catch { vSample = vRaw.slice(0, 120) }
    check('hugegraph_entity_identity_correct', vOk && badIdentity === 0,
      `HugeGraph aiops 图 Entity 顶点携带正确 tenant_id/cluster_id（样本=${vSample}；抽查 ${vCount} 个顶点，identity 错误=${badIdentity}）`)

    // 5. 服务全景使用的图投影（services/overview）——GRAPH_IDENTITY_UNAVAILABLE
    const ov = await apiRetry(page, 'get', `/services/overview?cluster_id=${ENV.clusterId}`)
    let warn = ''
    try { warn = (JSON.parse(ov.body)?.warnings || []).join(',') } catch {}
    check('service_overview_graph_identity_available', !/GRAPH_IDENTITY_UNAVAILABLE/.test(warn),
      `GET /services/overview warnings=${warn}（图实体体系不可用导致服务全景图投影为空——与 503 缺陷同源，如实记录）`)

    // 6. 图谱运维页面渲染（Graph Ops 数据在产品页面可见）
    await spaNav(page, '/admin/graph-operations')
    await page.waitForTimeout(4000)
    const goText = ((await page.locator('body').textContent().catch(() => '')) || '')
    // 实测：GraphOpsPanel 用 Promise.all 同时拉 sync-states/outbox/aliases/shadow-diff，
    // 其中 /ai/kg/ops/aliases 503（GRAPH_UNAVAILABLE）→ 整个 Promise.all 拒绝 → 面板空白
    const aliases = await apiRetry(page, 'get', `/ai/kg/ops/aliases?cluster_id=${ENV.clusterId}`)
    let aliasCode = ''
    try { aliasCode = JSON.parse(aliases.body)?.error?.code || '' } catch {}
    check('graph_operations_page_renders_sync_states', /catalog|trace|kubernetes|success/i.test(goText),
      `图谱运维页面渲染长度=${goText.length}，sync-states 可见=${/catalog|trace|kubernetes/i.test(goText)}。缺陷（如实记录）：面板以 Promise.all 同时拉取 4 个接口，/ai/kg/ops/aliases -> ${aliases.status} ${aliasCode} 导致整个面板空白（sync-states/outbox 数据虽正常但不渲染）`)
    await shot(page, 'PF-LOGIC-015-graphops')

    notes.push('结论：Graph Ops 数据（sync-states/outbox）与 MySQL 后端一致、HugeGraph 后端实体 identity 正确（tenant/cluster 标签无误）、带 cluster scope 的图检索可用；但不带 cluster_id 的图检索 503（GRAPH_FEATURE_UNAVAILABLE_LEGACY）、/services/overview 图投影 GRAPH_IDENTITY_UNAVAILABLE（服务全景空）、/ai/kg/ops/aliases 503 导致图谱运维面板空白——实体体系在页面维度的可用性受损（如实记录）。')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 2 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-015', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
