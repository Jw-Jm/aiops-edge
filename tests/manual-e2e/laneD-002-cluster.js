// PF-UI-002 ClusterSwitcher：集群列表 / 切换动作 / 当前 scope 与服务端一致 / 无残留。
// 真实行为（实测）：/auth/login 后服务端 active_scope 为空、Switcher 显示占位符；
// 用户在下拉选择集群后 onChange -> setActiveScope -> 服务端 scope 与页面数据全部按该集群过滤。
// 本环境仅 1 个集群（kubernetes-cluster），"切换到另一个集群"无法执行，限制记 notes。
// Standalone: node tests/manual-e2e/laneD-002-cluster.js
const { ENV, uiLogin, spaNav, makeCollector, writeResult, shot, withSession } = require('./lib/laneD-runner')

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const result = {
    id: 'PF-UI-002', route: '/overview (topbar ClusterSwitcher)', category: 'pages',
    consoleErrors: [], pageErrors: [], failedRequests: [], scopeViolations: [],
    checks: [], notes: [], clusterOptions: [],
  }
  result.notes.push(`单集群环境：仅 ${ENV.clusterName}（${ENV.clusterId}），无法切换到另一个集群；切换动作以"未选择 -> 选中 kubernetes-cluster"的真实 onChange 验证，单集群限制记 notes。`)
  const check = (name, pass, detail = '') => result.checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 300) })

  const col = makeCollector(tag)
  await withSession({ viewport: vp, col, seedScope: false }, async (page) => {
    // PF-UI-002 专门测试 ClusterSwitcher：不预置 scope（seedScope=false，不门闸），
    // 走"登录后未选择 -> 用户在下拉选择集群"的真实 UI 流程。withSession 已完成 UI 登录（429 退避）。
    await page.waitForTimeout(1500)

    // 初始状态（记录，不判失败）：登录后 scope 未选中
    const me0 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
    const initialScope = me0?.active_scope?.cluster_id || me0?.data?.active_scope?.cluster_id || ''
    const sel0 = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    result.notes.push(`初始态：服务端 active_scope.cluster_id="${initialScope}"，选择器显示="${sel0 || '(placeholder 选择作用域)'}"——登录后会话 scope 未选中，需用户显式选择。`)

    // 打开下拉：集群列表
    await page.locator('.topbar .ant-select').first().click()
    await page.waitForTimeout(800)
    const dropdown = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden)')
    const optTexts = await dropdown.locator('.ant-select-item-option').allTextContents()
    result.clusterOptions = optTexts
    check('cluster_list_shows_expected_clusters', optTexts.length >= 1 && optTexts.some((t) => t.includes(ENV.clusterName)), JSON.stringify(optTexts))
    check('cluster_list_no_unauthorized_entries', optTexts.every((t) => t.includes(ENV.clusterName)), JSON.stringify(optTexts))

    // 切换动作：选择集群（初始为未选中 -> 选中，真实 onChange 触发）
    await dropdown.locator('.ant-select-item-option').first().click()
    await page.waitForTimeout(1500)
    const selAfter = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    check('switcher_shows_selected_cluster', selAfter.includes(ENV.clusterName), `text="${selAfter}"`)

    // 当前 scope 与服务端一致
    const me1 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
    const scope1 = me1?.active_scope?.cluster_id || me1?.data?.active_scope?.cluster_id || ''
    check('server_scope_matches_selected_cluster', scope1 === ENV.clusterId, `active_scope.cluster_id=${scope1}`)

    // 客户端 scope 持久化
    const ls = await page.evaluate(() => {
      try { return JSON.parse(localStorage.getItem('aiops-ui-v3'))?.state?.currentClusterId || '' } catch { return '' }
    })
    check('client_scope_persisted', ls === ENV.clusterId, `localStorage.currentClusterId=${ls}`)

    // 切换后页面数据更新且 scope 正确（SPA 重新进入数据页）
    const mark = col.scopeViolations.length
    await spaNav(page, '/overview'); await page.waitForTimeout(2500)
    await spaNav(page, '/alerts/events'); await page.waitForTimeout(2500)
    await spaNav(page, '/overview'); await page.waitForTimeout(1500)
    const newViolations = col.scopeViolations.slice(mark)
    check('page_data_requests_scoped_after_switch', newViolations.length === 0, JSON.stringify(newViolations.slice(0, 3)))
    const scoped = col.requests.filter((r) => r.clusterId === ENV.clusterId)
    check('requests_carry_expected_cluster_id', scoped.length > 0, `scoped=${scoped.length}`)
    // 无残留：不允许出现其它具体集群的 cluster_id；cluster_id=all 仅允许出现在
    // 未选择集群时的 Overview 资源回退请求（前端 currentClusterId||'all' 的产品行为）
    const selectionTime = Math.min(...col.requests.filter((r) => r.clusterId === ENV.clusterId).map((r) => r.t), Infinity)
    const otherConcrete = col.requests.filter((r) => r.clusterId && r.clusterId !== ENV.clusterId && r.clusterId !== 'all')
    check('no_other_cluster_ids_in_requests', otherConcrete.length === 0, JSON.stringify(otherConcrete.slice(0, 3).map((r) => r.url)))
    const allScoped = col.requests.filter((r) => r.clusterId === 'all')
    const allAfterSelection = allScoped.filter((r) => r.t > selectionTime)
    if (allScoped.length) {
      result.notes.push(`cluster_id=all 请求 ${allScoped.length} 个（未选择集群时 Overview 资源卡片的 'all' 回退）；选中集群后仍为 all 的请求：${allAfterSelection.length} 个。`)
      check('no_stale_unscoped_data_after_selection', allAfterSelection.length === 0,
        JSON.stringify(allAfterSelection.slice(0, 3).map((r) => r.url)))
    } else {
      check('no_stale_unscoped_data_after_selection', true, 'no cluster_id=all requests at all')
    }
    const overviewText = (await page.locator('main').textContent()) || ''
    check('page_renders_after_switch', overviewText.length > 100, `main text length=${overviewText.length}`)

    await shot(page, `PF-UI-002-${tag}`)
    result.consoleErrors.push(...col.consoleErrors)
    result.pageErrors.push(...col.pageErrors)
    result.failedRequests.push(...col.badResponses)
    result.scopeViolations = col.scopeViolations
  })

  const summary = writeResult('PF-UI-002', 'pages', result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((e) => { console.error(e); process.exit(1) })
module.exports = { run }
