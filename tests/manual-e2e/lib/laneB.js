// Lane B shared helpers: summary jsonl append + API run/evidence discovery.
const fs = require('fs')
const path = require('path')
const { ROOT } = require('./harness')

const SUMMARY = path.join(ROOT, 'pages', 'laneB-summary.jsonl')
const BASE = 'http://localhost:30253'
const TENANT = '7ed01afc-cc79-4ecd-8767-a2befa6168ad'
const CLUSTER = '91771a6e-9c2d-11f1-8271-bea176fe9f9f'

function record(summary) {
  fs.mkdirSync(path.dirname(SUMMARY), { recursive: true })
  fs.appendFileSync(SUMMARY, JSON.stringify(summary) + '\n')
  console.log(JSON.stringify(summary))
}

// cfg.notes: extra notes always appended to the recorded summary (also passed
// through to harness when supported). cfg.setup(page): per-viewport pre-actions.
async function runLaneB(cfg) {
  const { runPageTest, ENV } = require('./harness')
  const setup = cfg.setup
  const cfg2 = { ...cfg, notes: cfg.notes || [] }
  if (setup) {
    cfg2.actions = async (page, check, tag) => {
      // authStore 是内存态：若仍停在 /login（含 harness 登录竞态失败的情况），
      // 通过真实 UI 登录，再重设 scope 并回到目标路由。
      await page.waitForTimeout(1200)
      if (/\/login/.test(page.url())) {
        await uiLogin(page)
        await page.waitForTimeout(500)
      }
      // UI 登录产生新会话 cookie → 重新绑定 tenant/cluster scope（幂等）
      await page.request.post(`${ENV.apiBase}/me/scope`, {
        data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
      })
      if (!page.url().includes(cfg.route)) {
        await page.goto(`${ENV.baseURL}${cfg.route}`, { waitUntil: 'domcontentloaded' })
        await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
      }
      await setup(page, check, tag)
    }
  }
  try {
    const s = await runPageTest(cfg2)
    s.notes = [...new Set([...(s.notes || []), ...cfg2.notes])]
    if (cfg.mutate) cfg.mutate(s)
    record(s)
    return s
  } catch (e) {
    const s = {
      id: cfg.id, route: cfg.route, status: 'ERROR',
      failedChecks: ['runner_exception'], error: String(e).slice(0, 500),
      notes: [...(cfg2.notes || [])],
    }
    record(s)
    return s
  }
}

// Login via real API, set scope, return cookie string.
async function apiCookie() {
  const login = await fetch(`${BASE}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: 'admin1234' }),
  })
  if (!login.ok) throw new Error(`login failed ${login.status}`)
  const cookies = (login.headers.getSetCookie?.() || []).map((c) => c.split(';')[0])
  const cookie = cookies.join('; ')
  await fetch(`${BASE}/api/v1/me/scope`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', cookie },
    body: JSON.stringify({ tenant_id: TENANT, cluster_id: CLUSTER }),
  })
  return cookie
}

// Find latest run that has at least one evidence (per manual PF-PAGE-015/016 precondition).
async function findLatestRunWithEvidence() {
  const cookie = await apiCookie()
  const runsResp = await fetch(`${BASE}/api/v1/ai/runs?limit=5`, { headers: { cookie } })
  if (!runsResp.ok) return null
  const runs = (await runsResp.json())?.runs || []
  const q = `tenant_id=${TENANT}&cluster_id=${CLUSTER}`
  for (const r of runs) {
    const ev = await fetch(`${BASE}/api/v1/ai/runs/${r.run_id}/evidences?${q}`, { headers: { cookie } })
    if (!ev.ok) continue
    const body = await ev.json()
    const list = body?.evidences || []
    if (list.length) return { run: r, evidence: list[0], count: list.length }
  }
  return null
}

// UI 登录（带 429 退避重试：后端限流 10 次/60s/user+IP，多 lane 共享预算）
async function uiLogin(page) {
  for (let attempt = 0; attempt < 4; attempt++) {
    if (attempt > 0) await new Promise((res) => setTimeout(res, 15000))
    await page.getByPlaceholder('用户名').fill('admin')
    await page.getByPlaceholder('密码').fill('admin1234')
    await page.getByRole('button', { name: /登\s*录/ }).click()
    const ok = await page
      .waitForURL((u) => !String(u).includes('/login'), { timeout: 10000 })
      .then(() => true).catch(() => false)
    if (ok) {
      await page.request.post(`${BASE}/api/v1/me/scope`, {
        data: { tenant_id: TENANT, cluster_id: CLUSTER },
      })
      return true
    }
  }
  throw new Error('uiLogin failed (rate-limited or form error)')
}

// SPA 内部导航（pushState，不整页刷新——整页刷新会丢失内存态 authStore 回到 /login）
async function gotoApp(page, route) {
  if (/\/login/.test(page.url())) await uiLogin(page)
  await page.evaluate((u) => {
    window.history.pushState({}, '', u)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, route)
  await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
}

// Select an antd option from a dropdown triggered by `trigger`.
async function pickOption(page, trigger, optionText) {
  await trigger.click()
  const opt = page
    .locator(`.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option`, { hasText: optionText })
    .last()
  await opt.waitFor({ state: 'visible', timeout: 5000 })
  await opt.click()
}

module.exports = { record, runLaneB, findLatestRunWithEvidence, apiCookie, pickOption, uiLogin, gotoApp, BASE, TENANT, CLUSTER }
