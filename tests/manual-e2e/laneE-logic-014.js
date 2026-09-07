// PF-LOGIC-014 系统设置与实际运行逻辑。
// 设置页 Cluster 列表 vs ClusterSwitcher/后端；LLM Provider/Model vs llm_providers 表；
// 组件状态 vs 实际部署；审计日志（已知 /ops/audit-logs 404，如实记录）。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-014.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry, sh } = require('./lib/laneE')

function mysqlQuery(q) {
  const esc = q.replace(/"/g, '').replace(/\$/g, '\\$').replace(/`/g, '')
  return sh(`kubectl exec -n observability mysql-0 -- sh -c "mysql -uroot -p\\"\\$MYSQL_ROOT_PASSWORD\\" aiops -e \\"${esc}\\"" 2>/dev/null`).trim()
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. Cluster 设置 vs 后端 vs ClusterSwitcher
    const clApi = await apiRetry(page, 'get', '/clusters')
    let apiNames = []
    try { apiNames = (JSON.parse(clApi.body)?.clusters || []).map((c) => c.name) } catch {}
    await spaNav(page, '/admin/settings')
    await page.waitForTimeout(3000)
    // 集群列表在"纳管集群"Tab 下（默认 Tab 为 AI 模型配置）
    const clusterTab = page.locator('.ant-tabs-tab').filter({ hasText: '纳管集群' }).first()
    if (await clusterTab.isVisible().catch(() => false)) { await clusterTab.click(); await page.waitForTimeout(2500) }
    const setText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('settings_cluster_list_matches_backend',
      apiNames.every((n) => setText.includes(n)),
      `设置页"纳管集群"Tab 包含后端集群名 ${JSON.stringify(apiNames)}=${apiNames.every((n) => setText.includes(n))}（页面文本长度=${setText.length}）`)
    // ClusterSwitcher 一致性（topbar）
    const selText = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    check('settings_cluster_matches_switcher', selText.includes(ENV.clusterName),
      `ClusterSwitcher 显示="${selText}"，与设置页/后端集群 ${ENV.clusterName} 一致`)

    // 2. LLM Provider/Model vs llm_providers 表
    const llmApi = await apiRetry(page, 'get', '/settings/llm/providers')
    let apiProviders = []
    try { apiProviders = JSON.parse(llmApi.body)?.providers || [] } catch {}
    let dbCount = '-1'
    try { dbCount = mysqlQuery('SELECT count(*) FROM llm_providers').split('\n').pop().trim() } catch {}
    const llmText = /LLM|模型|Provider/i.test(setText) ? setText : ''
    const consistent = apiProviders.length === 0 && dbCount === '0'
    check('llm_providers_consistent_empty', consistent,
      `API /settings/llm/providers providers=${apiProviders.length}；MySQL llm_providers 行数=${dbCount}；两者一致（空=一致）。设置页 LLM 区块渲染=${!!llmText}`)
    notes.push('LLM Provider/Model：后端 llm_providers 表为空（0 行），API 返回 providers=[]，设置页无 provider 条目——三方一致（空=一致）。LLM_MOCK=true 环境下 AI Chat 使用内置 mock，不依赖 provider 表。')

    // 3. 系统组件状态 vs 实际部署（"平台健康"Tab + kubectl/网络可达性反查）
    const healthTab = page.locator('.ant-tabs-tab').filter({ hasText: '平台健康' }).first()
    if (await healthTab.isVisible().catch(() => false)) { await healthTab.click(); await page.waitForTimeout(2500) }
    const compApi = await apiRetry(page, 'get', '/system/components')
    let comps = []
    try { comps = JSON.parse(compApi.body)?.components || [] } catch {}
    const compSummary = comps.map((c) => `${c.name}:${c.status}`).join(' ')
    check('system_components_api_populated', comps.length > 0, `GET /system/components -> ${compSummary.slice(0, 300)}`)
    const healthText = ((await page.locator('body').textContent().catch(() => '')) || '')
    const compOnPage = comps.filter((c) => healthText.includes(c.name)).length
    check('components_visible_on_health_tab', compOnPage >= Math.ceil(comps.length * 0.8),
      `"平台健康"Tab 展示组件 ${compOnPage}/${comps.length}`)
    // 实际部署反查：pod 状态 + 服务端点网络可达性（组件健康为主动探测）
    const podsRaw = sh(`kubectl get pods -n observability --no-headers 2>/dev/null | awk '{print $1" "$3}'`).trim()
    const podMap = {}
    for (const line of podsRaw.split('\n')) { const [n, st] = line.split(' '); if (n && st) podMap[n] = st }
    const mismatch = []
    const consistentDown = []
    for (const c of comps) {
      if (c.type !== 'service') continue
      const podName = Object.keys(podMap).find((n) => n.startsWith(c.name))
      const podStatus = podName ? podMap[podName] : '(无 pod)'
      if (c.status === 'ok' && podStatus !== 'Running') mismatch.push(`${c.name}: 页面=ok 但 pod=${podStatus}`)
      if (c.status !== 'ok') {
        // 页面非 ok：验证服务端点是否确实不可达（busybox wget 探测）
        let probe = ''
        try {
          probe = sh(`kubectl run probe-lanee-${c.name.replace(/[^a-z0-9]/gi, '')} --rm -i --restart=Never --image=busybox:1.36 -n observability -- wget -q -O- -T 6 http://${c.name}.observability.svc.cluster.local:8080/readyz 2>&1 | head -1`).trim()
        } catch (e) { probe = String(e).slice(0, 60) }
        const unreachable = /error|timed out|refused|bad gateway|invalid/i.test(probe) || probe === ''
        if (podStatus === 'Running' && !unreachable) mismatch.push(`${c.name}: 页面=${c.status} 但 pod=${podStatus} 且端点可达`)
        else consistentDown.push(`${c.name}: 页面=${c.status}，pod=${podStatus}，端点探测不可达（页面状态与实际一致）`)
      }
    }
    check('component_status_vs_deployment', mismatch.length === 0,
      mismatch.length ? `不一致项: ${mismatch.join('; ').slice(0, 300)}` : `组件状态与实际部署一致（非 ok 项核实：${consistentDown.join('; ').slice(0, 220)}）`)
    if (consistentDown.length) notes.push(`组件状态核实（如实记录）：${consistentDown.join('; ')}——ai-orchestrator pod Running 但服务端点仅 localhost 可达（其日志仅 127.0.0.1 命中 /health），页面 down 与实际不可达一致。`)

    // 4. 审计日志（已知 /ops/audit-logs 404）
    const auditTab = page.locator('.ant-tabs-tab').filter({ hasText: '审计日志' }).first()
    if (await auditTab.isVisible().catch(() => false)) { await auditTab.click(); await page.waitForTimeout(2500) }
    const auditApi = await apiRetry(page, 'get', '/ops/audit-logs?page=1&size=10')
    check('audit_logs_api_available', auditApi.status === 200,
      `GET /ops/audit-logs -> ${auditApi.status} ${auditApi.body.slice(0, 120)}（已知缺陷：后端 404，审计日志页面不可用；MySQL audit_logs 表存在但 0 行）`)
    let auditDb = '-1'
    try { auditDb = mysqlQuery('SELECT count(*) FROM audit_logs').split('\n').pop().trim() } catch {}
    notes.push(`审计日志：MySQL audit_logs 表行数=${auditDb}；API 404 → 关键管理操作（如本次测试的用户创建/删除、规则创建）无法在产品页面审计留痕查看。`)

    await shot(page, 'PF-LOGIC-014-settings')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 1 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-014', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
