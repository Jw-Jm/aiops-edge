// PF-LOGIC-002 多 Cluster 产品逻辑。
// 验证：ClusterSwitcher 显示与列表、scope 与 /me 一致、页面请求带正确 cluster 参数，
// 并在已纳管的第二集群上完成真实切换与隔离核对。
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

    const clusterResponse = await page.request.get(`${ENV.apiBase}/clusters`)
    const clusterBody = await clusterResponse.json().catch(() => ({}))
    const clusterRows = Array.isArray(clusterBody) ? clusterBody : (clusterBody?.clusters || clusterBody?.data || [])
    const primary = clusterRows.find((c) => c.cluster_id === ENV.clusterId) || { cluster_id: ENV.clusterId, name: ENV.clusterName }
    const secondary = clusterRows.find((c) => c.cluster_id && c.cluster_id !== ENV.clusterId)
    check('server_exposes_two_canonical_clusters', clusterResponse.ok() && Boolean(secondary),
      `clusters=${clusterRows.map((c) => `${c.name}:${c.cluster_id}`).join(', ')}`)

    // 打开 ClusterSwitcher 下拉
    await page.locator('.topbar .ant-select').first().click()
    await page.waitForTimeout(800)
    const dropdown = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)')
    const optTexts = await dropdown.locator('.ant-select-item-option').allTextContents()
    check('switcher_lists_server_clusters', optTexts.length >= 2 && optTexts.some((t) => t.includes(primary.name)),
      `下拉选项=${JSON.stringify(optTexts)}`)

    // 选择主集群（真实 onChange）
    await dropdown.locator('.ant-select-item-option').filter({ hasText: primary.name }).first().click()
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

    // 各业务页面使用当前主 Cluster：请求携带正确 cluster_id
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

    // 选择第二个真实纳管集群并再次核对 scope 与请求隔离
    if (secondary) {
      await page.locator('.topbar .ant-select').first().click()
      await page.waitForTimeout(500)
      const secondDropdown = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)')
      await secondDropdown.locator('.ant-select-item-option').filter({ hasText: secondary.name }).first().click()
      await page.waitForTimeout(1200)
      const me2 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
      const secondScope = me2?.active_scope?.cluster_id || ''
      check('multi_cluster_switch_verified', secondScope === secondary.cluster_id,
        `切换到 ${secondary.name} 后 active_scope.cluster_id=${secondScope}`)
      const secondMark = col.requests.length
      await spaNav(page, '/overview'); await page.waitForTimeout(1800)
      const secondRequests = col.requests.slice(secondMark).filter((r) => r.method === 'GET' && r.path.startsWith('/api/v1/'))
      const secondBad = secondRequests.filter((r) => r.clusterId && r.clusterId !== secondary.cluster_id && r.clusterId !== 'all')
      check('second_cluster_requests_are_isolated', secondBad.length === 0,
        `second=${secondary.cluster_id} bad=${JSON.stringify(secondBad.slice(0, 3).map((r) => r.url))}`)
      notes.push(`已在 ${secondary.name}（${secondary.cluster_id}）完成真实 ClusterSwitcher 切换与请求隔离核对。`)
    } else {
      check('multi_cluster_switch_verified', false, '未发现第二个 canonical 纳管集群')
      check('second_cluster_requests_are_isolated', false, '未发现第二个 canonical 纳管集群')
    }

    await shot(page, 'PF-LOGIC-002-switcher')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : 'FAIL'
  notes.push(`通过 ${pass}/${checks.length} 项；第二集群必须来自真实 canonical Cluster Registry。`)
  writeResult('PF-LOGIC-002', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
