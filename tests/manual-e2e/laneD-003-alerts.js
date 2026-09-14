// PF-UI-003 告警通知与 Badge：Badge 数量合理且与数据一致 / 通知下拉 / 暂无告警态 / 查看全部跳转 / 30s 刷新。
// Standalone: node tests/manual-e2e/laneD-003-alerts.js
const { ENV, uiLogin, spaNav, makeCollector, writeResult, shot, withSession } = require('./lib/laneD-runner')

// 与 App.tsx loadAlerts 相同的统计口径：未 resolved 事件按 object 展开行数
function expectedBadge(events) {
  const active = (events || []).filter((e) => e.status !== 'resolved')
  return active.reduce((sum, e) => {
    const objs = String(e?.object || '').split(',').map((s) => s.trim()).filter(Boolean)
    return sum + (objs.length || 1)
  }, 0)
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const result = {
    id: 'PF-UI-003', route: '/overview (topbar bell + sidebar badge)', category: 'pages',
    consoleErrors: [], pageErrors: [], failedRequests: [], checks: [], notes: [], dropdown: {},
  }
  const check = (name, pass, detail = '') => result.checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 300) })

  const col = makeCollector(tag)
  await withSession({ viewport: vp, col }, async (page) => {
    // withSession 已完成 UI 登录（429 退避）+ /me/scope + scope 预置
    await page.waitForTimeout(2000)

    // 服务端告警数据 → 期望 badge 值
    const resp = await page.request.get(`${ENV.apiBase}/alerts/events?limit=200&cluster_id=${ENV.clusterId}`)
    const body = await resp.json().catch(() => ({}))
    const list = Array.isArray(body) ? body : (body?.events ?? body?.data ?? [])
    const expected = expectedBadge(list)
    result.notes.push(`服务端未解决告警展开行数（期望 badge）= ${expected}；事件条数 = ${list.length}`)

    // 侧栏 badge
    const badgeLoc = page.locator('.nav__item', { hasText: '告警事件' }).locator('.nav__badge')
    const badgeCount = await badgeLoc.count()
    const badgeText = badgeCount > 0 ? ((await badgeLoc.first().textContent()) || '').trim() : ''
    const badgeNum = badgeText === '' ? 0 : Number(badgeText)
    check('badge_value_matches_server_count', badgeCount > 0 && Number.isFinite(badgeNum) && badgeNum === expected,
      `badge="${badgeText}" expected=${expected}`)
    check('badge_count_reasonable', Number.isFinite(badgeNum) && badgeNum >= 0 && badgeNum <= 999, `badge=${badgeNum}`)

    // 通知下拉
    await page.locator('.topbar__icon-btn').first().click()
    await page.waitForTimeout(900)
    const panel = page.locator('.ant-dropdown:not(.ant-dropdown-hidden)')
    const panelVisible = await panel.isVisible().catch(() => false)
    const headerOk = panelVisible && (await panel.locator('text=告警通知').count()) > 0
    check('notification_dropdown_opens', panelVisible && headerOk)
    const emptyState = (await panel.locator('text=暂无告警').count()) > 0
    const items = await panel.locator('[data-testid="notification-alert-item"]').count()
    result.dropdown = { visible: panelVisible, emptyState, itemCount: items, expected }
    if (expected === 0) {
      check('empty_alert_state_shown', emptyState, `暂无告警 visible=${emptyState}`)
    } else {
      check('notification_items_shown', items > 0, `items=${items}`)
      check('empty_alert_state_n/a', true, '存在未解决告警，暂无告警态不适用')
    }
    // ping 点与 badge 同步
    const pingShown = (await page.locator('.topbar__icon-btn .ping').count()) > 0
    check('bell_ping_dot_matches_badge', pingShown === (badgeNum > 0), `ping=${pingShown} badge=${badgeNum}`)
    await shot(page, `PF-UI-003-${tag}-dropdown`)

    // 查看全部跳转
    await panel.locator('text=查看全部').first().click()
    await page.waitForTimeout(1200)
    check('view_all_navigates_to_alert_events', page.url().includes('/alerts/events'), `url=${page.url()}`)

    // 30 秒刷新：回 /overview，观察 32s 内 /alerts/events 是否再次被拉取
    await spaNav(page, '/overview')
    await page.waitForTimeout(1000)
    const t0 = Date.now()
    const alertsBefore = col.requests.filter((r) => r.path === '/api/v1/alerts/events').length
    await page.waitForTimeout(33000)
    const alertsAfter = col.requests.filter((r) => r.path === '/api/v1/alerts/events').length
    check('alerts_refreshed_within_30s_window', alertsAfter > alertsBefore,
      `before=${alertsBefore} after=${alertsAfter} waited=${Date.now() - t0}ms`)

    result.consoleErrors.push(...col.consoleErrors)
    result.pageErrors.push(...col.pageErrors)
    result.failedRequests.push(...col.badResponses)
  })

  const summary = writeResult('PF-UI-003', 'pages', result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((e) => { console.error(e); process.exit(1) })
module.exports = { run }
