// PF-FLOW-001 登录→Cluster→首页→服务→Trace→日志 跨页面真实业务闭环（全程只读）。
// 复用 laneD-runner 成熟原语：真实 UI 登录（429 退避）+ pushState/popstate SPA 导航
// + route gate + localStorage(aiops-ui-v3) 集群预注入 + 请求级 cluster scope 审计。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneG-flow-001.js
const fs = require('fs')
const path = require('path')
const { ENV, ROOT, spaNav, makeCollector, withSession, shot, GLOBAL_PATHS } = require('./lib/laneD-runner')

const ID = 'PF-FLOW-001'
const TARGET_SERVICE = 'payments' // 手册指定服务；步骤4先用 /dashboard/stats top_services 确认

function writeFlow(steps, notes, status) {
  const dir = path.join(ROOT, 'flows')
  fs.mkdirSync(dir, { recursive: true })
  const out = { id: ID, steps, status, notes }
  fs.writeFileSync(path.join(dir, `${ID}.json`), JSON.stringify(out, null, 2))
  console.log(JSON.stringify({ id: ID, status, failed: steps.filter((s) => !s.pass).map((s) => s.step) }))
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const steps = []
  const notes = []
  const step = (name, pass, detail = '') => steps.push({ step: name, pass: !!pass, detail: String(detail).slice(0, 600) })
  const col = makeCollector(tag)
  let mark = 0
  const newReqs = () => col.requests.slice(mark).filter((r) => r.method === 'GET' && r.path.startsWith('/api/v1/') && r.status < 400)
  const sampleUrls = (rs, n = 3) => rs.slice(0, n).map((r) => r.url).join('  ||  ')
  const advance = () => { mark = col.requests.length }

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // ---- Step1 UI 登录 ----
    const loggedIn = !/\/login/.test(page.url()) && (await page.locator('.sidebar').count()) > 0
    const loginReqs = newReqs().filter((r) => /auth\/login|me\/scope/.test(r.path))
    step('1_UI登录', loggedIn, `url=${page.url()}；登录/scope请求=${loginReqs.length ? sampleUrls(loginReqs, 2) : '(POST,不计GET)'}；consoleErrors=${col.consoleErrors.length}`)
    advance()

    // ---- Step2 选择集群 kubernetes-cluster ----
    await page.waitForTimeout(1200)
    const selText = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    const me = await page.request.get(`${ENV.apiBase}/me`).then((r) => r.json()).catch(() => ({}))
    const serverScope = me?.active_scope?.cluster_id || me?.data?.active_scope?.cluster_id || ''
    const lsCluster = await page.evaluate(() => {
      try { return JSON.parse(localStorage.getItem('aiops-ui-v3'))?.state?.currentClusterId || '' } catch { return '' }
    })
    step('2_选择集群kubernetes-cluster', selText.includes(ENV.clusterName) && serverScope === ENV.clusterId && lsCluster === ENV.clusterId,
      `switcher="${selText}" serverScope=${serverScope} localStorage=${lsCluster}`)
    advance()

    // ---- Step3 /overview 统计卡有数据 ----
    await spaNav(page, '/overview')
    await page.waitForTimeout(3000)
    const statLabels = ['服务数量', '调用总量', '请求错误率', 'P95 延迟']
    const statDetail = []
    let statsOk = true
    for (const label of statLabels) {
      const card = page.locator('.stat', { has: page.locator('.stat__label', { hasText: label }) }).first()
      const cnt = await card.count()
      let val = ''
      if (cnt > 0) val = ((await card.locator('.stat__value').first().textContent().catch(() => '')) || '').trim()
      const ok = cnt > 0 && val !== '' && val !== '-' && val !== '…'
      if (!ok) statsOk = false
      statDetail.push(`${label}=${val || '(空)'}`)
    }
    // /dashboard/stats 请求可能在登录落地 /overview 时已发出，从全程请求日志取证
    const statsReq = col.requests.find((r) => r.path.includes('/dashboard/stats'))
    const overviewReqs = newReqs().length ? newReqs() : col.requests.filter((r) => r.method === 'GET' && r.path.startsWith('/api/v1/') && r.status < 400).slice(-4)
    step('3_首页统计卡有数据', statsOk && !!statsReq,
      `${statDetail.join(' ')}；关键请求: ${sampleUrls(overviewReqs)}`)
    advance()

    // ---- Step4 服务全景选定真实服务（先 API 确认 top_services）----
    const statsBody = await page.request.get(`${ENV.apiBase}/dashboard/stats?cluster_id=${ENV.clusterId}`)
      .then((r) => r.json()).catch(() => ({}))
    const top = (statsBody?.top_services || []).map((s) => s.service)
    const svc = top.includes(TARGET_SERVICE) ? TARGET_SERVICE : (top[0] || TARGET_SERVICE)
    if (svc !== TARGET_SERVICE) notes.push(`top_services 无 ${TARGET_SERVICE}，回退到 top_services[0]=${svc}；top=${JSON.stringify(top)}`)
    await spaNav(page, '/observability/service')
    await page.waitForTimeout(3500)
    let mainText = (await page.locator('main').textContent().catch(() => '')) || ''
    // 环境实况：默认时间范围(近1小时)内无服务数据（/services/map minutes=60 → 0 服务），
    // 切换时间范围到"近 24 小时"后服务出现（只读 UI 交互）
    let rangeSwitched = false
    if (!mainText.includes(svc)) {
      const trSelect = page.locator('.ant-select', { has: page.locator('.ant-select-selection-item', { hasText: '近 1 小时' }) }).first()
      if ((await trSelect.count()) > 0) {
        await trSelect.click()
        await page.waitForTimeout(800)
        const opt = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option', { hasText: '近 24 小时' }).first()
        if ((await opt.count()) > 0) {
          await opt.click()
          rangeSwitched = true
          await page.waitForTimeout(4000)
          mainText = (await page.locator('main').textContent().catch(() => '')) || ''
        }
      }
    }
    const svcReqs = newReqs()
    const svcApiReq = svcReqs.find((r) => r.path.includes('/services'))
    step('4_服务全景选定服务', mainText.includes(svc) && !!svcApiReq,
      `目标服务=${svc}（top_services 确认）；页面含服务名=${mainText.includes(svc)}；时间范围切到24h=${rangeSwitched}；关键请求: ${sampleUrls(svcReqs)}`)
    advance()

    // ---- Step5 /observability/trace 查该服务 ----
    await spaNav(page, '/observability/trace')
    await page.waitForTimeout(2500)
    // 打开"按服务筛选"下拉并选择目标服务
    let svcFilterOk = false
    const svcSelect = page.locator('.ant-select', { has: page.locator('.ant-select-selection-placeholder', { hasText: '按服务筛选' }) }).first()
    if ((await svcSelect.count()) > 0) {
      await svcSelect.click()
      await page.waitForTimeout(800)
      const opt = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option', { hasText: svc }).first()
      if ((await opt.count()) > 0) { await opt.click(); svcFilterOk = true }
      else notes.push(`Trace 服务下拉无 ${svc} 选项`)
    } else {
      notes.push('未找到"按服务筛选"下拉')
    }
    await page.waitForTimeout(3000)
    let traceRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    let traceDetail = `服务筛选选中=${svcFilterOk}；时间范围=近24小时(默认)；表格行=${traceRows}`
    let traceReq = newReqs().filter((r) => r.path.endsWith('/traces'))
    // 兜底1：时间范围 24h 无数据 → trace_id 搜索（API 侧 7d=168h 拿一个真实 trace_id）
    if (traceRows === 0) {
      const t = await page.request.get(`${ENV.apiBase}/traces?cluster_id=${ENV.clusterId}&service=${svc}&hours=168&limit=1`)
        .then((r) => r.json()).catch(() => ({}))
      const tid = t?.data?.[0]?.trace_id || ''
      notes.push(`24h UI 表格 0 行，API 7d 兜底 trace_id=${tid || '(无)'}`)
      if (tid) {
        const searchBox = page.getByPlaceholder(/搜索|trace/i).first()
        if ((await searchBox.count()) > 0) {
          await searchBox.fill(tid)
          await searchBox.press('Enter')
          await page.waitForTimeout(2500)
          traceRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
          traceDetail += `；trace_id搜索后行=${traceRows}`
          traceReq = newReqs().filter((r) => r.path.endsWith('/traces'))
        }
      }
    }
    // 下钻一条 Trace 验证 identity（瀑布图 span 服务名）
    let drillOk = false
    let drillDetail = '未下钻'
    try {
      const firstLink = page.locator('.ant-table-tbody tr.ant-table-row a').first()
      if ((await firstLink.count()) > 0 && traceRows > 0) {
        await firstLink.click()
        await page.waitForTimeout(2500)
        const drawer = page.locator('.ant-drawer').first()
        const drawerText = (await drawer.textContent().catch(() => '')) || ''
        drillOk = (await drawer.count()) > 0 && drawerText.length > 50
        drillDetail = `drawer打开=${(await drawer.count()) > 0} 含服务名=${drawerText.includes(svc)}`
        await page.keyboard.press('Escape')
        await page.waitForTimeout(500)
      }
    } catch (e) { drillDetail = `下钻异常: ${String(e).slice(0, 120)}` }
    step('5_Trace查该服务', svcFilterOk && traceRows > 0 && drillOk,
      `${traceDetail}；${drillDetail}；关键请求: ${sampleUrls(traceReq)}`)
    advance()

    // ---- Step6 /observability/log 查该服务日志 ----
    await spaNav(page, '/observability/log')
    await page.waitForTimeout(3000)
    let logRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    // UI 关键词筛选（页面无独立 service 下拉，用关键词=服务名走 /logs/query?query=）
    let kwRows = -1
    const logSearch = page.getByPlaceholder(/搜索关键词/).first()
    if ((await logSearch.count()) > 0) {
      await logSearch.fill(svc)
      await logSearch.press('Enter')
      await page.waitForTimeout(3000)
      kwRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    }
    // service 筛选兜底：页面同源 API /logs/query?service=<svc>（后端支持，UI 未暴露 service 下拉）
    const logSvc = await page.request.get(`${ENV.apiBase}/logs/query?cluster_id=${ENV.clusterId}&service=${svc}&hours=24&limit=20`)
      .then((r) => r.json()).catch(() => ({}))
    const logSvcRows = logSvc?.data || []
    const allSvcMatch = logSvcRows.length > 0 && logSvcRows.every((r) => (r.service_name || r.service) === svc)
    const logReqs = newReqs().filter((r) => r.path.includes('/logs/'))
    step('6_日志查该服务', logRows > 0 && allSvcMatch,
      `日志表行=${logRows}；关键词"${svc}"筛选后行=${kwRows}；API service筛选 ${logSvcRows.length} 行且 service_name 全=${svc}=${allSvcMatch}（UI 无 service 下拉，service 筛选经页面同源 /logs/query 验证）；关键请求: ${sampleUrls(logReqs)}`)
    advance()

    // ---- Step7 全程 identity 与 cluster scope 一致 ----
    const violations = []
    const allNotes = []
    for (const r of col.requests) {
      if (r.method !== 'GET' || !r.path.startsWith('/api/v1/') || r.status >= 400) continue
      const sub = r.path.replace('/api/v1', '')
      const isGlobal = GLOBAL_PATHS.some((p) => sub === p || sub.startsWith(p + '/') || sub.startsWith(p)) ||
        /^\/ai\/runs\/[^/]+\/(events|evidences|tools)/.test(sub)
      if (isGlobal) continue
      if (!r.clusterId) violations.push({ url: r.url, reason: 'missing cluster_id' })
      else if (r.clusterId !== ENV.clusterId && r.clusterId !== 'all') violations.push({ url: r.url, reason: `cluster_id=${r.clusterId}` })
      else if (r.clusterId === 'all') allNotes.push(r.url)
    }
    if (allNotes.length) notes.push(`cluster_id=all 请求 ${allNotes.length} 个（前端 currentClusterId||'all' 回退路径）：${allNotes.slice(0, 2).join(' | ')}`)
    // identity 一致性：同一服务名贯穿 services/trace/logs
    const identityOk = mainText.includes(svc) && svcFilterOk && allSvcMatch
    step('7_identity与cluster scope一致', violations.length === 0 && identityOk,
      `scope违规=${violations.length} ${JSON.stringify(violations.slice(0, 3))}；服务identity "${svc}" 贯穿 服务全景/Trace筛选/日志service筛选=${identityOk}；全程 cluster_id=${ENV.clusterId}`)

    await shot(page, `${ID}-${tag}`)
  })

  const failed = steps.filter((s) => !s.pass)
  writeFlow(steps, notes, failed.length === 0 ? 'PASS' : 'FAIL')
}

run().catch((e) => {
  writeFlow([{ step: 'runner', pass: false, detail: String(e).slice(0, 500) }], ['runner crashed'], 'FAIL')
  process.exit(1)
})
