// PF-LOGIC-015 Graph 数据与产品页面逻辑。
// 使用 active session scope + typed graph APIs 验证 Graph health、实体搜索、运维状态
// 和服务全景投影，不能把旧的 503 预期当成通过条件。
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
    // 1. 图读接口：active session scope 是授权事实，浏览器不必重复提交 cluster_id。
    const search = await apiRetry(page, 'get', `/ai/kg/entities/search?q=payments&entity_type=service&limit=20&cluster_id=${ENV.clusterId}`)
    let entity = null
    try { entity = (JSON.parse(search.body)?.items || [])[0] } catch {}
    check('graph_entity_search_available', search.status === 200 && entity?.name === 'payments',
      `GET /ai/kg/entities/search(cluster_id) -> ${search.status}，实体=${entity ? `${entity.entity_type}:${entity.name} tenant=${entity.tenant_id} cluster=${entity.cluster_id}` : search.body.slice(0, 120)}`)
    const searchNoScope = await apiRetry(page, 'get', '/ai/kg/entities/search?q=payments&entity_type=service&limit=20')
    let scopedEntity = null
    try { scopedEntity = (JSON.parse(searchNoScope.body)?.items || [])[0] } catch {}
    check('graph_search_uses_active_session_scope', searchNoScope.status === 200 && scopedEntity?.name === 'payments',
      `active session scope search -> ${searchNoScope.status} entity=${scopedEntity?.name || 'missing'}`)

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

    // 5. 服务全景使用 typed graph projection；若确有退化，只允许明确的可解释 warning。
    const ov = await apiRetry(page, 'get', `/services/overview?cluster_id=${ENV.clusterId}`)
    let warn = ''
    try { warn = (JSON.parse(ov.body)?.warnings || []).join(',') } catch {}
    check('service_overview_graph_projection_contract', ov.status === 200 && !/LEGACY|UNKNOWN|INTERNAL_ERROR/i.test(warn),
      `GET /services/overview status=${ov.status} warnings=${warn || 'none'}`)

    // 6. 图谱运维页面渲染（Graph Ops 数据在产品页面可见）
    await spaNav(page, '/admin/graph-operations')
    await page.waitForTimeout(4000)
    const goText = ((await page.locator('body').textContent().catch(() => '')) || '')
    const aliases = await apiRetry(page, 'get', `/ai/kg/ops/aliases?cluster_id=${ENV.clusterId}`)
    let aliasCode = ''
    try { aliasCode = JSON.parse(aliases.body)?.error?.code || '' } catch {}
    check('graph_operations_page_renders_sync_states', /catalog|trace|kubernetes|success/i.test(goText),
      `图谱运维页面渲染长度=${goText.length}，sync-states 可见=${/catalog|trace|kubernetes/i.test(goText)}；aliases=${aliases.status} ${aliasCode}`)
    await shot(page, 'PF-LOGIC-015-graphops')

    notes.push(`Graph contract: active-scope search=${searchNoScope.status}, typed search=${search.status}, overview=${ov.status}, aliases=${aliases.status}; warnings=${warn || 'none'}`)
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 2 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-015', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
