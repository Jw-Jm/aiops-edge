// PF-UI-001 全局导航与布局：七大导航板块 / 菜单跳转 / 高亮 / 侧栏收展 / 顶栏 / AI 运维助手入口。
// Run standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneD-001-nav.js
const { ENV, uiLogin, spaNav, makeCollector, writeResult, shot, withSession } = require('./lib/laneD-runner')

// Real nav structure from observability-frontend/src/App.tsx NAV_GROUPS (7 groups, 19 items)
const NAV_GROUPS = [
  { title: '总览', items: [{ path: '/overview', label: '工作台首页' }] },
  { title: '可观测', items: [
    { path: '/observability/service', label: '服务全景' },
    { path: '/observability/relationships', label: '资源关系' },
    { path: '/observability/trace', label: '链路追踪' },
    { path: '/observability/log', label: '日志与指标' },
    { path: '/observability/vms', label: '虚拟机' },
    { path: '/observability/grafana', label: 'Grafana 面板' },
  ] },
  { title: '告警', items: [
    { path: '/alerts/events', label: '告警事件' },
    { path: '/alerts/rules', label: '告警规则' },
  ] },
  { title: '智能调查', items: [{ path: '/investigation', label: '调查中心' }] },
  { title: '容量与资源', items: [
    { path: '/capacity', label: '容量预测' },
    { path: '/infra/k8s', label: 'K8s 运维' },
    { path: '/hardware', label: '硬件健康' },
  ] },
  { title: '报告', items: [
    { path: '/report', label: '报告中心' },
    { path: '/changes', label: '变更时间线' },
  ] },
  { title: '系统管理', footer: true, items: [
    { path: '/admin/approvals', label: '审批中心' },
    { path: '/admin/users', label: '用户管理' },
    { path: '/admin/settings', label: '系统设置' },
    { path: '/admin/graph-operations', label: '图谱运维' },
  ] },
]
const ALL_ITEMS = NAV_GROUPS.flatMap((g) => g.items)

async function run() {
  const result = {
    id: 'PF-UI-001', route: '(global layout)', category: 'pages',
    consoleErrors: [], pageErrors: [], failedRequests: [], scopeViolations: [],
    checks: [], notes: [], navClickResults: [],
  }
  const check = (name, pass, detail = '') => result.checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 300) })

  for (const vp of ENV.viewports) {
    const tag = `${vp.width}x${vp.height}`
    const col = makeCollector(tag)
    await withSession({ viewport: vp, col }, async (page) => {
      // withSession 已完成 UI 登录（429 退避）+ /me/scope + scope 预置
      const urlAfterLogin = page.url()
      check(`${tag}_login_reaches_layout`, /\/overview/.test(urlAfterLogin) && !/\/login/.test(urlAfterLogin), urlAfterLogin)

      // 1) 七大导航板块
      for (const g of NAV_GROUPS) {
        const visible = await page.locator('.nav__group-label', { hasText: g.title }).first().isVisible().catch(() => false) ||
          (await page.locator('.nav__footer .nav__group-label', { hasText: g.title }).count()) > 0
        check(`${tag}_group_${g.title}`, visible)
      }
      // 19 个菜单项齐全
      for (const it of ALL_ITEMS) {
        const n = await page.locator('.nav__item', { hasText: it.label }).count()
        check(`${tag}_item_${it.label}`, n >= 1, `count=${n}`)
      }

      // 2) 所有菜单跳转 + 3) 当前菜单高亮
      for (const it of ALL_ITEMS) {
        await page.locator('.nav__item', { hasText: it.label }).first().click()
        await page.waitForTimeout(900)
        const url = page.url()
        const navOk = url.replace(/\/$/, '') === `${ENV.baseURL}${it.path}`
        const activeTexts = await page.locator('.nav__item.is-active').allTextContents()
        const activeOk = activeTexts.some((t) => t.includes(it.label))
        check(`${tag}_nav_${it.path}`, navOk && activeOk, `url=${url} active=${JSON.stringify(activeTexts)}`)
        result.navClickResults.push({ viewport: tag, path: it.path, url, activeTexts, ok: navOk && activeOk })
        await spaNav(page, '/overview')
        await page.waitForTimeout(300)
      }

      // 4) 侧栏展开/收起
      await page.waitForTimeout(500)
      const wBefore = (await page.locator('.sidebar').boundingBox())?.width
      await page.locator('.nav__collapse-btn').click()
      await page.waitForTimeout(600)
      const wCollapsed = (await page.locator('.sidebar').boundingBox())?.width
      const labelsHidden = (await page.locator('.nav__item span:visible').count()) === 0
      await page.locator('.nav__collapse-btn').click()
      await page.waitForTimeout(600)
      const wRestored = (await page.locator('.sidebar').boundingBox())?.width
      check(`${tag}_sidebar_collapse`, wBefore > 200 && wCollapsed <= 80 && labelsHidden && wRestored > 200,
        `before=${wBefore} collapsed=${wCollapsed} restored=${wRestored} labelsHidden=${labelsHidden}`)

      // 5) 顶栏
      const topbarVisible = await page.locator('.topbar').isVisible()
      const clusterSelect = await page.locator('.topbar .ant-select').count()
      const clock = (await page.locator('.topbar__clock').textContent().catch(() => '')) || ''
      // user-chip 显示登录用户（displayName「管理员」或用户名 admin）
      const userChip = await page.locator('.topbar .user-chip').count()
      const chipText001 = (await page.locator('.topbar .user-chip').textContent().catch(() => '')) || ''
      const bell = await page.locator('.topbar__icon-btn').count()
      check(`${tag}_topbar`, topbarVisible && clusterSelect >= 1 && clock.trim().length > 0 && userChip >= 1 && bell >= 1 && /(admin|管理员)/.test(chipText001),
        `topbar=${topbarVisible} select=${clusterSelect} clock="${clock.trim().slice(0, 30)}" userChip=${userChip} chip="${chipText001.trim().slice(0, 20)}" bell=${bell}`)

      // 6) AI 运维助手入口（侧栏 hero + AiDock 悬浮球）
      const hero = page.locator('.nav__hero', { hasText: 'AI 运维助手' })
      const heroVisible = await hero.isVisible().catch(() => false)
      await hero.click()
      await page.waitForTimeout(900)
      const chatUrl = page.url()
      check(`${tag}_ai_entry_nav_hero`, heroVisible && chatUrl.includes('/ai/chat'), `url=${chatUrl}`)
      await spaNav(page, '/overview')
      await page.waitForTimeout(500)
      const dockBtn = await page.locator('.ai-dock__btn').isVisible()
      check(`${tag}_ai_entry_dock`, dockBtn)
      if (dockBtn) {
        await page.locator('.ai-dock__btn').click()
        await page.waitForTimeout(500)
        const panel = await page.locator('.ai-dock__panel', { hasText: 'AI 运维助手' }).isVisible().catch(() => false)
        check(`${tag}_ai_dock_panel_opens`, panel)
        await page.keyboard.press('Escape')
        await page.waitForTimeout(300)
      }
      await shot(page, `PF-UI-001-${tag}`)
      result.consoleErrors.push(...col.consoleErrors)
      result.pageErrors.push(...col.pageErrors)
      result.failedRequests.push(...col.badResponses)
    })
  }

  const summary = writeResult('PF-UI-001', 'pages', result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((e) => { console.error(e); process.exit(1) })
module.exports = { run }
