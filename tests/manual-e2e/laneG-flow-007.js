// PF-FLOW-007 系统设置→ClusterSwitcher→各页面（全程只读）。
// 步骤：/admin/settings 核对集群列表与详情（节点/Namespace/Event）→ ClusterSwitcher 当前集群
// → 逐页 /overview /observability/trace /alerts/events /investigation /infra/k8s 确认 cluster scope
// → 无跨集群串扰；集群详情必须通过 canonical cluster_id 进入身份校验边界。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneG-flow-007.js
const fs = require('fs')
const path = require('path')
const { ENV, ROOT, spaNav, makeCollector, withSession, shot, GLOBAL_PATHS } = require('./lib/laneD-runner')

const ID = 'PF-FLOW-007'
const PAGES = ['/overview', '/observability/trace', '/alerts/events', '/investigation', '/infra/k8s']

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
  const sampleUrls = (rs, n = 4) => rs.slice(0, n).map((r) => r.url).join('  ||  ')
  const advance = () => { mark = col.requests.length }
  const pageScope = {} // route -> 关键请求 URL 样本（写入 detail 证据文件）

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // ---- Step1 /admin/settings 集群列表与详情 ----
    await spaNav(page, '/admin/settings')
    await page.waitForTimeout(2000)
    // 顶部 Tabs：AI 模型配置 / 纳管集群 / 审计日志 / 平台健康 —— 切到"纳管集群"
    const clusterTab = page.locator('.ant-tabs-tab', { hasText: '纳管集群' }).first()
    if ((await clusterTab.count()) > 0) { await clusterTab.click(); await page.waitForTimeout(1500) }
    const row = page.locator('.ant-table-tbody tr.ant-table-row', { hasText: ENV.clusterName }).first()
    const rowText = (await row.textContent().catch(() => '')) || ''
    const listOk = (await row.count()) > 0 && rowText.includes(ENV.clusterName) && /active|ready|healthy/i.test(rowText)
    // 打开详情（查看）
    let detailOk = false
    const detailInfo = []
    if ((await row.count()) > 0) {
      await row.getByText('查看').first().click().catch(() => {})
      await page.waitForTimeout(2000)
      const modal = page.locator('.ant-modal').first()
      detailOk = (await modal.count()) > 0
      // 三个详情 Tab：节点 / 命名空间 / 事件
      for (const [key, label] of [['nodes', '节点'], ['namespaces', '命名空间'], ['events', '事件']]) {
        const tab = page.locator('.ant-tabs-tab', { hasText: label }).first()
        if ((await tab.count()) > 0) {
          await tab.click()
          await page.waitForTimeout(1800)
          const tabText = (await tab.textContent().catch(() => '')) || ''
          const alert = page.locator('.ant-modal .ant-alert-warning').first()
          const alertText = (await alert.textContent().catch(() => '')) || ''
          detailInfo.push(`${label}:${tabText.replace(/\s+/g, '')}${alertText ? ` Alert="${alertText.replace(/\s+/g, ' ').slice(0, 120)}"` : ''}`)
        }
      }
      const setReqs = newReqs().filter((r) => /\/clusters\/[^/]+\/(nodes|namespaces|events)/.test(r.path))
      step('1_系统设置集群列表与详情', listOk && detailOk && detailInfo.length === 3,
        `列表行="${rowText.replace(/\s+/g, ' ').slice(0, 140)}"；详情打开=${detailOk}；${detailInfo.join(' | ')}；关键请求: ${sampleUrls(setReqs)}`)
      // 关闭弹窗
      await page.keyboard.press('Escape')
      await page.waitForTimeout(600)
    } else {
      step('1_系统设置集群列表与详情', false, `未找到 ${ENV.clusterName} 集群行`)
    }
    advance()

    // ---- Step2 ClusterSwitcher 当前集群 ----
    const selText = ((await page.locator('.topbar .ant-select-selection-item').first().textContent().catch(() => '')) || '').trim()
    const me = await page.request.get(`${ENV.apiBase}/me`).then((r) => r.json()).catch(() => ({}))
    const serverScope = me?.active_scope?.cluster_id || me?.data?.active_scope?.cluster_id || ''
    const lsCluster = await page.evaluate(() => {
      try { return JSON.parse(localStorage.getItem('aiops-ui-v3'))?.state?.currentClusterId || '' } catch { return '' }
    })
    step('2_ClusterSwitcher当前集群', selText.includes(ENV.clusterName) && serverScope === ENV.clusterId && lsCluster === ENV.clusterId,
      `switcher="${selText}" serverScope=${serverScope} localStorage=${lsCluster}`)
    advance()

    // ---- Step3 逐页确认 cluster scope ----
    for (const route of PAGES) {
      await spaNav(page, route)
      await page.waitForTimeout(3000)
      const reqs = newReqs()
      const mainText = (await page.locator('main').textContent().catch(() => '')) || ''
      const scoped = reqs.filter((r) => r.clusterId === ENV.clusterId)
      const unscopedGlobal = reqs.filter((r) => !r.clusterId)
      const bad = reqs.filter((r) => r.clusterId && r.clusterId !== ENV.clusterId && r.clusterId !== 'all')
      pageScope[route] = reqs.slice(0, 6).map((r) => r.url)
      // 渲染判定：正常数据渲染 或 显式空态（本环境 /alerts/events 0 事件 → "暂无告警"空态属真实数据状态）
      const renderOk = mainText.trim().length > 100 || /暂无|加载中|empty/i.test(mainText)
      step(`3_页面${route}`, renderOk && bad.length === 0 && (scoped.length > 0 || unscopedGlobal.length > 0),
        `渲染长度=${mainText.trim().length}${renderOk && mainText.trim().length <= 100 ? '(显式空态)' : ''}；scoped请求=${scoped.length} 全局请求=${unscopedGlobal.length} 错误scope=${bad.length}；关键请求: ${sampleUrls(reqs)}`)
      advance()
    }

    // ---- Step4 无跨集群数据串扰 ----
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
    if (allNotes.length) notes.push(`cluster_id=all 请求 ${allNotes.length} 个（前端 currentClusterId||'all' 回退）：${allNotes.slice(0, 2).join(' | ')}`)
    notes.push('所有集群级请求均须携带当前 canonical cluster_id；跨集群切换与数据隔离由 PF-LOGIC-002 使用第二个真实纳管集群完成。')
    step('4_无跨集群数据串扰', violations.length === 0,
      `scope违规=${violations.length} ${JSON.stringify(violations.slice(0, 3))}；其它具体cluster_id请求=0`)

    await shot(page, `${ID}-${tag}`)
  })

  // 证据文件：每页关键请求 URL
  fs.writeFileSync(path.join(ROOT, 'flows', `${ID}-detail.json`), JSON.stringify({ id: ID, pageScope, totalRequests: col.requests.length }, null, 2))

  const failed = steps.filter((s) => !s.pass)
  const status = failed.length === 0 ? 'PASS' : 'FAIL'
  writeFlow(steps, notes, status)
}

run().catch((e) => {
  writeFlow([{ step: 'runner', pass: false, detail: String(e).slice(0, 500) }], ['runner crashed'], 'FAIL')
  process.exit(1)
})
