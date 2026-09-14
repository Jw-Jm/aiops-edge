// PF-UI-005 全页面显示质量（26 路由 x 2 viewport：无白屏 / 无横向溢出 / 关键控件可见）
// PF-UI-006 Console 与 Network 卫生（同一遍历收集：console.error/pageerror/4xx-5xx/请求循环/scope/密钥）
// Standalone: node tests/manual-e2e/laneD-005-pages.js
const { ENV, uiLogin, spaNav, makeCollector, writeResult, shot, withSession } = require('./lib/laneD-runner')

// 手册 §7 的 26 个页面（PF-PAGE-001~026）对应的真实路由
const ROUTES = [
  { page: 'PF-PAGE-001', route: '/login', chrome: 'login', label: '登录页' },
  { page: 'PF-PAGE-002', route: '/change-password', chrome: 'bare', label: '首次修改密码' },
  { page: 'PF-PAGE-003', route: '/overview', chrome: 'layout', label: '工作台首页' },
  { page: 'PF-PAGE-004', route: '/observability/service', chrome: 'layout', label: '服务全景' },
  { page: 'PF-PAGE-005', route: '/observability/relationships', chrome: 'layout', label: '资源关系' },
  { page: 'PF-PAGE-006', route: '/observability/trace', chrome: 'layout', label: '链路追踪' },
  { page: 'PF-PAGE-007', route: '/observability/log', chrome: 'layout', label: '日志与指标' },
  { page: 'PF-PAGE-008', route: '/observability/vms', chrome: 'layout', label: '虚拟机' },
  { page: 'PF-PAGE-009', route: '/observability/grafana', chrome: 'layout', label: 'Grafana 面板' },
  { page: 'PF-PAGE-010', route: '/alerts/events', chrome: 'layout', label: '告警事件' },
  { page: 'PF-PAGE-011', route: '/alerts/rules', chrome: 'layout', label: '告警规则' },
  { page: 'PF-PAGE-012', route: '/ai/chat', chrome: 'layout', label: 'AI 运维助手' },
  { page: 'PF-PAGE-013', route: '/investigation', chrome: 'layout', label: '调查中心' },
  { page: 'PF-PAGE-014', route: '/investigation/new', chrome: 'layout', label: '发起调查' },
  { page: 'PF-PAGE-015', route: '/investigation/__RUN__', chrome: 'layout', label: '调查详情', dynamic: true },
  { page: 'PF-PAGE-016', route: '/investigation/__RUN__/evidence/__EVID__', chrome: 'layout', label: 'Evidence 详情', dynamic: true },
  { page: 'PF-PAGE-017', route: '/capacity', chrome: 'layout', label: '容量预测' },
  { page: 'PF-PAGE-018', route: '/infra/k8s', chrome: 'layout', label: 'K8s 运维' },
  { page: 'PF-PAGE-019', route: '/hardware', chrome: 'layout', label: '硬件健康' },
  { page: 'PF-PAGE-020', route: '/report', chrome: 'layout', label: '报告中心' },
  { page: 'PF-PAGE-021', route: '/changes', chrome: 'layout', label: '变更时间线' },
  { page: 'PF-PAGE-022', route: '/admin/approvals', chrome: 'layout', label: '审批中心' },
  { page: 'PF-PAGE-023', route: '/admin/users', chrome: 'layout', label: '用户管理' },
  { page: 'PF-PAGE-024', route: '/admin/settings', chrome: 'layout', label: '系统设置' },
  { page: 'PF-PAGE-025', route: '/admin/graph-operations', chrome: 'layout', label: '图谱运维' },
  { page: 'PF-PAGE-026', route: '/__e2e_not_found__', chrome: 'layout', label: '404 页面' },
]

async function run() {
  const ui5 = { id: 'PF-UI-005', category: 'pages', pages: [], checks: [], notes: [] }
  const ui6 = {
    id: 'PF-UI-006', category: 'pages', checks: [], notes: [],
    consoleErrors: [], pageErrors: [], badResponses: [], requestLoops: [],
    scopeViolations: [], secretHits: [],
  }

  for (const vp of ENV.viewports) {
    const tag = `${vp.width}x${vp.height}`
    const col = makeCollector(tag)
    await withSession({ viewport: vp, col }, async (page) => {
      // withSession(seedScope=true) 已完成：UI 登录（429 退避）+ POST /me/scope
      // + localStorage scope 预置（与合并后的 harness.runPageTest 行为一致）
      // 动态路由参数：真实 run/evidence id（只读查询，不创建数据）
      let runId = '__e2e_no_run__'
      let evidenceId = ''
      try {
        const runsResp = await page.request.get(`${ENV.apiBase}/ai/runs?limit=1`)
        if (runsResp.ok()) {
          const rb = await runsResp.json()
          const runs = rb?.runs || rb?.data?.runs || []
          if (runs.length && runs[0]?.run_id) runId = runs[0].run_id
        }
      } catch {}
      if (runId !== '__e2e_no_run__') {
        try {
          const evResp = await page.request.get(`${ENV.apiBase}/ai/runs/${runId}/evidences?tenant_id=${ENV.tenantId}&cluster_id=${ENV.clusterId}`)
          if (evResp.ok()) {
            const eb = await evResp.json()
            const evs = eb?.evidences || eb?.data?.evidences || []
            if (evs.length && evs[0]?.id) evidenceId = String(evs[0].id)
          }
        } catch {}
      }
      if (!evidenceId) evidenceId = 'e2e-no-evidence'
      ui5.notes.push(`viewport ${tag}: investigation run_id=${runId}, evidence_id=${evidenceId}（无 run/evidence 时使用占位符，页面应渲染错误/空态而非崩溃）`)

      // 记录 scope 就绪时间点（seed 模式下应接近 0，无未 scope 的 403 爆发）
      const scopeSelectedAt = Math.min(
        ...col.requests.filter((q) => q.clusterId === ENV.clusterId).map((q) => q.t), Infinity)
      ui5.notes.push(`viewport ${tag}: scope 就绪于 t=${scopeSelectedAt}ms`)

      for (const r of ROUTES) {
        const target = r.route.replace('__RUN__', runId).replace('__EVID__', evidenceId)
        const markStart = col.requests.length
        const errStart = { c: col.consoleErrors.length, p: col.pageErrors.length, b: col.badResponses.length }
        const entry = { page: r.page, label: r.label, route: target, viewport: tag, checks: {} }
        try {
          await spaNav(page, target)
          await page.waitForLoadState('networkidle', { timeout: 10000 }).catch(() => {})
          await page.waitForTimeout(1200)

          const finalUrl = page.url()
          const redirectedToLogin = finalUrl.includes('/login') && !target.startsWith('/login')
          entry.finalUrl = finalUrl

          // 无白屏：DOM 有实质内容
          const bodyLen = await page.evaluate(() => document.body.innerText.trim().length)
          entry.checks.no_white_screen = bodyLen > 20
          // 无横向溢出
          const overflow = await page.evaluate(() => ({
            sw: document.documentElement.scrollWidth, iw: window.innerWidth,
          }))
          entry.checks.no_horizontal_overflow = overflow.sw <= overflow.iw + 2
          entry.overflow = overflow
          // 关键控件可见
          if (r.chrome === 'layout') {
            const sidebar = await page.locator('.sidebar').isVisible().catch(() => false)
            const topbar = await page.locator('.topbar').isVisible().catch(() => false)
            const mainChildren = await page.locator('main > *').count()
            entry.checks.key_controls_visible = sidebar && topbar && mainChildren > 0 && !redirectedToLogin
          } else if (r.chrome === 'login') {
            entry.checks.key_controls_visible = (await page.locator('input[placeholder="用户名"]').count()) > 0
          } else {
            entry.checks.key_controls_visible = bodyLen > 20
          }
          entry.checks.not_redirected_to_login = !redirectedToLogin
          await shot(page, `PF-UI-005-${r.page}-${tag}`)
        } catch (e) {
          entry.error = String(e).slice(0, 300)
          entry.checks.no_white_screen = false
          entry.checks.no_horizontal_overflow = false
          entry.checks.key_controls_visible = false
        }
        // PF-UI-006 数据按路由切片
        entry.consoleErrors = col.consoleErrors.slice(errStart.c).map((x) => x.text)
        entry.pageErrors = col.pageErrors.slice(errStart.p).map((x) => x.text)
        entry.badResponses = col.badResponses.slice(errStart.b)
        entry.badResponsesPreSelection = col.badResponses.slice(errStart.b).filter((x) => x.t < scopeSelectedAt)
        entry.badResponsesPostSelection = col.badResponses.slice(errStart.b).filter((x) => x.t >= scopeSelectedAt)
        const win = col.requests.slice(markStart)
        const counts = {}
        for (const q of win) {
          const k = `${q.method} ${q.url}`
          counts[k] = (counts[k] || 0) + 1
        }
        entry.requestLoopCandidates = Object.entries(counts).filter(([, n]) => n > 20).map(([url, n]) => ({ url, count: n }))
        ui5.pages.push(entry)
      }
      ui6.scopeSelectedAt = ui6.scopeSelectedAt || {}
      ui6.scopeSelectedAt[tag] = scopeSelectedAt
      ui6.consoleErrors.push(...col.consoleErrors)
      ui6.pageErrors.push(...col.pageErrors)
      ui6.badResponses.push(...col.badResponses)
      ui6.scopeViolations.push(...col.scopeViolations)
      ui6.secretHits.push(...col.secretHits)
      ui6.requestLoops.push(...col.detectLoops().map((x) => ({ ...x, viewport: tag })))
    })
  }

  // ---- PF-UI-005 汇总 ----
  for (const p of ui5.pages) {
    for (const [k, v] of Object.entries(p.checks)) {
      ui5.checks.push({ name: `${p.page}_${p.viewport}_${k}`, pass: !!v, detail: JSON.stringify({ route: p.route, overflow: p.overflow, error: p.error }).slice(0, 300) })
    }
  }
  ui5.checks.push({ name: 'routes_covered_26', pass: ui5.pages.length === 52, detail: `pages entries=${ui5.pages.length} (26 routes x 2 viewports)` })

  // ---- PF-UI-006 汇总 ----
  const allLoopCandidates = ui5.pages.flatMap((p) => p.requestLoopCandidates.map((x) => ({ ...x, route: p.route, viewport: p.viewport })))
  // 分类：scope 选择完成（t < scopeSelectedAt）前的 4xx/5xx 与 console 错误属于
  // "登录后未选择集群"初始态（后端 fail-closed 403，选择集群后恢复），记为 explained；
  // 选择之后仍出现的错误为未解释，按真实结果判定。
  const sel = (e) => ui6.scopeSelectedAt?.[e.viewport] ?? Infinity
  const preBad = ui6.badResponses.filter((b) => b.t < sel(b))
  const postBad = ui6.badResponses.filter((b) => b.t >= sel(b))
  const preConsole = ui6.consoleErrors.filter((b) => b.t < sel(b))
  const postConsole = ui6.consoleErrors.filter((b) => b.t >= sel(b))
  // 占位符 evidence 深链（环境无 evidence 数据时的测试占位）造成的 404 记为 explained
  const evidencePlaceholder = ui5.notes.some((n) => n.includes('evidence_id=e2e-no-evidence'))
  const evidenceArtifact = (b) => evidencePlaceholder && /\/evidences\/e2e-no-evidence/.test(b.url)
  const explainedBad = postBad.filter(evidenceArtifact)
  const unexplainedBad = postBad.filter((b) => !evidenceArtifact(b))
  ui6.explained = {
    preSelectionBadResponses: preBad,
    preSelectionConsoleErrors: preConsole,
    evidencePlaceholderArtifact404: explainedBad.map((b) => `${b.url} -> ${b.status}`),
  }
  ui6.checks.push({ name: 'no_unexplained_console_errors', pass: postConsole.length === 0, detail: JSON.stringify(postConsole.slice(0, 5)) })
  ui6.checks.push({ name: 'no_page_errors', pass: ui6.pageErrors.length === 0, detail: JSON.stringify(ui6.pageErrors.slice(0, 5)) })
  ui6.checks.push({ name: 'no_unexplained_4xx_5xx', pass: unexplainedBad.length === 0, detail: JSON.stringify(unexplainedBad.slice(0, 8)) })
  ui6.checks.push({ name: 'no_infinite_request_loop', pass: allLoopCandidates.length === 0, detail: JSON.stringify(allLoopCandidates.slice(0, 5)) })
  ui6.checks.push({ name: 'requests_use_correct_cluster_scope', pass: ui6.scopeViolations.length === 0, detail: JSON.stringify(ui6.scopeViolations.slice(0, 5)) })
  ui6.checks.push({ name: 'no_sensitive_keys_in_browser', pass: ui6.secretHits.length === 0, detail: JSON.stringify(ui6.secretHits.slice(0, 5)) })
  ui6.notes.push('登录后未选择集群时，后端对数据查询 fail-closed 返回 403（产品初始态行为，通过 UI 选择集群后恢复）——该窗口内的 403 记为 explained，见 explained 字段。')

  const s5 = writeResult('PF-UI-005', 'pages', ui5)
  const s6 = writeResult('PF-UI-006', 'pages', ui6)
  console.log(JSON.stringify({ PF_UI_005: s5, PF_UI_006: s6 }))
  return { s5, s6 }
}

if (require.main === module) run().catch((e) => { console.error(e); process.exit(1) })
module.exports = { run }
