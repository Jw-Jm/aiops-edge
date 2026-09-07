// PF-LOGIC-002 多 Cluster 产品逻辑（单集群环境）。
// 验证：ClusterSwitcher 显示与列表、scope 与 /me 一致、页面请求带正确 cluster 参数。
// 单集群环境无法执行"切换到另一个集群"，多集群切换记 PARTIAL。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-002.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult } = require('./lib/laneE')

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: false }, async (page) => {
    // 不预置 scope：走"登录后未选择 → 用户在下拉选择集群"的真实流程
    await page.waitForTimeout(1500)
    const me0 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
    const initialScope = me0?.active_scope?.cluster_id || ''
    notes.push(`登录后初始服务端 active_scope.cluster_id="${initialScope}"（未选择状态，需用户显式选择）。`)

    // 打开 ClusterSwitcher 下拉
    await page.locator('.topbar .ant-select').first().click()
    await page.waitForTimeout(800)
    const dropdown = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)')
    const optTexts = await dropdown.locator('.ant-select-item-option').allTextContents()
    check('switcher_lists_server_clusters', optTexts.some((t) => t.includes(ENV.clusterName)),
      `下拉选项=${JSON.stringify(optTexts)}；服务端 /clusters 仅 1 个集群 kubernetes-cluster`)

    // 选择集群（真实 onChange）
    await dropdown.locator('.ant-select-item-option').first().click()
    await page.waitForTimeout(1500)
    const selAfter = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    check('switcher_shows_selected_cluster', selAfter.includes(ENV.clusterName), `选择器显示="${selAfter}"`)

    // scope 与 /me 一致
    const me1 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
    const scope1 = me1?.active_scope?.cluster_id || ''
    check('server_scope_matches_switcher', scope1 === ENV.clusterId, `active_scope.cluster_id=${scope1}`)
    const ls = await page.evaluate(() => {
      try { return JSON.parse(localStorage.getItem('aiops-ui-v3'))?.state?.currentClusterId || '' } catch { return '' }
    })
    check('client_scope_persisted', ls === ENV.clusterId, `localStorage.currentClusterId=${ls}`)

    // 各业务页面使用当前 Cluster：请求携带正确 cluster_id
    const mark = col.scopeViolations.length
    await spaNav(page, '/overview'); await page.waitForTimeout(2500)
    await spaNav(page, '/alerts/events'); await page.waitForTimeout(2500)
    await spaNav(page, '/observability/service'); await page.waitForTimeout(2500)
    const newViolations = col.scopeViolations.slice(mark)
    check('page_requests_scoped_correctly', newViolations.length === 0, JSON.stringify(newViolations.slice(0, 3)))
    const scoped = col.requests.filter((r) => r.clusterId === ENV.clusterId)
    check('requests_carry_expected_cluster_id', scoped.length > 0, `带 ${ENV.clusterId} 的请求=${scoped.length} 个`)
    const otherConcrete = col.requests.filter((r) => r.clusterId && r.clusterId !== ENV.clusterId && r.clusterId !== 'all')
    check('no_other_cluster_ids_in_requests', otherConcrete.length === 0, JSON.stringify(otherConcrete.slice(0, 3).map((r) => r.url)))

    // 多集群切换：单集群环境无法执行 → PARTIAL
    notes.push(`多集群切换无法验证：环境仅 1 个集群（kubernetes-cluster / ${ENV.clusterId}），"切换 Cluster 后数据同步刷新"与"新增 Run/Action 使用当前 Cluster 的跨集群一致性"仅能在单集群内验证 scope 正确性，多集群维度记 PARTIAL。`)
    check('multi_cluster_switch_verified', false, '单集群环境，无法切换到第二个集群（环境限制，非产品缺陷）')

    await shot(page, 'PF-LOGIC-002-switcher')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : 'PARTIAL'
  notes.push(`通过 ${pass}/${checks.length} 项；多集群切换子项因单集群环境记 PARTIAL。`)
  writeResult('PF-LOGIC-002', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
