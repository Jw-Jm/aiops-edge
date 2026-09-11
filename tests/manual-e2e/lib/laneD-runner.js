// Lane D shared runner (PF-UI-001~006).
// Key constraint discovered by probe: the SPA keeps its auth token only in
// memory (authStore, non-persisted). A page.goto with a valid cookie still
// redirects to /login. So every session must: login through the real UI once,
// then navigate client-side via history.pushState + popstate (react-router v6
// picks this up and mounts the route normally).
const fs = require('fs')
const path = require('path')
const { chromium } = require('playwright')
const { ENV, ensureDirs } = require('./harness')
// 与 harness.js 相同的 ROOT 解析（lib/ 相对仓库根），结果统一写 test-results/<RUN_ID>/
const RUN_ID = process.env.TEST_RUN_ID || 'nonllm-' + new Date().toISOString().replace(/[-:]/g, '').replace(/\..*/, 'Z')
const ROOT = path.resolve(__dirname, '..', '..', '..', 'test-results', RUN_ID)

// Launch the bundled Chromium when its build is cached; otherwise fall back to
// the system Chrome so a real-browser gate never silently degrades to a stub.
async function launchBrowser() {
  try {
    return await chromium.launch()
  } catch (error) {
    if (!/Executable doesn't exist|browserType\.launch/.test(String(error))) throw error
    return chromium.launch({ channel: 'chrome' })
  }
}

// Mirrors GLOBAL_PATHS in observability-frontend/src/api/client.ts — requests
// to these paths are cluster-agnostic and legitimately carry no cluster_id.
const GLOBAL_PATHS = [
  '/clusters', '/users', '/ops/audit-logs', '/ops/reports',
  '/ops/changes', '/node/health', '/ipmi', '/settings', '/auth', '/slo',
  '/ai/sessions', '/ai/session', '/ai/runs', '/ai/actions', '/ai/skills',
  '/ai/workflows', '/ai/flows', '/mcp', '/grafana', '/system',
  // session/scope/bootstrap endpoints are not cluster-filtered either
  '/me', '/catalog', '/dashboard/panels', '/nodes/metrics', '/capacity/instances',
]

const SECRET_PATTERNS = [
  { name: 'openai_style_key', re: /sk-[a-zA-Z0-9]{20,}/ },
  { name: 'aws_access_key', re: /AKIA[0-9A-Z]{16}/ },
  { name: 'github_pat', re: /gh[pousr]_[A-Za-z0-9]{20,}/ },
  { name: 'slack_token', re: /xox[bpars]-[A-Za-z0-9-]{10,}/ },
  { name: 'google_api_key', re: /AIza[0-9A-Za-z\-_]{30,}/ },
  { name: 'private_key_block', re: /-----BEGIN [A-Z ]*PRIVATE KEY-----/ },
]

// UI 登录（带 429 退避）：POST /auth/login 限流 10/60s，且多个 lane 共用同一
// admin 账号——对齐 harness.runPageTest 的重试策略（4 次，间隔 15s）。
async function uiLogin(page, { expectSidebar = true } = {}) {
  await page.goto(`${ENV.baseURL}/login`, { waitUntil: 'domcontentloaded', timeout: 30000 })
  let loggedIn = false
  for (let attempt = 0; attempt < 4 && !loggedIn; attempt++) {
    if (attempt > 0) await new Promise((res) => setTimeout(res, 15000))
    await page.getByPlaceholder('用户名').fill(ENV.username)
    await page.getByPlaceholder('密码').fill(ENV.password)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    loggedIn = await page
      .waitForURL((u) => !String(u).includes('/login'), { timeout: 10000 })
      .then(() => true)
      .catch(() => false)
  }
  if (!loggedIn) throw new Error('UI login failed (rate-limited or form error)')
  if (expectSidebar) await page.waitForSelector('.sidebar', { timeout: 20000 })
  await page.waitForTimeout(1000)
  return page.url()
}

// Client-side navigation that react-router v6 handles like a normal in-app click.
async function spaNav(page, route) {
  await page.evaluate((r) => {
    window.history.pushState({}, '', r)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, route)
}

// Collector for console/pageerror/HTTP statuses/request log + secret scan.
function makeCollector(tag) {
  const col = {
    tag,
    consoleErrors: [], pageErrors: [], badResponses: [],
    requests: [], // { method, url, path, status, t, clusterId }
    scopeViolations: [], secretHits: [],
    _t0: Date.now(),
  }
  col.attach = (page) => {
    page.on('console', (m) => {
      if (m.type() === 'error') col.consoleErrors.push({ viewport: tag, text: m.text().slice(0, 500), t: Date.now() - col._t0 })
    })
    page.on('pageerror', (e) => col.pageErrors.push({ viewport: tag, text: String(e).slice(0, 500), t: Date.now() - col._t0 }))
    page.on('response', async (r) => {
      const url = r.url()
      const method = r.request().method()
      const status = r.status()
      let pathname = ''
      try { pathname = new URL(url).pathname } catch {}
      const u = new URL(url)
      const clusterId = u.searchParams.get('cluster_id') || ''
      const entry = { method, url: url.slice(0, 300), path: pathname, status, t: Date.now() - col._t0, clusterId }
      col.requests.push(entry)
      if (status >= 400) col.badResponses.push({ viewport: tag, method, url: url.slice(0, 300), status, t: entry.t })
      // cluster scope audit: cluster-level GET endpoints must carry correct cluster_id
      if (method === 'GET' && pathname.startsWith('/api/v1/') && status < 400) {
        const sub = pathname.replace('/api/v1', '')
        const isGlobal = GLOBAL_PATHS.some((p) => sub === p || sub.startsWith(p + '/') || sub.startsWith(p)) ||
          /^\/ai\/runs\/[^/]+\/(events|evidences|tools)/.test(sub)
        if (!isGlobal && !clusterId) {
          col.scopeViolations.push({ viewport: tag, url: url.slice(0, 300), reason: 'missing cluster_id on cluster-level GET' })
        } else if (clusterId && clusterId !== ENV.clusterId) {
          col.scopeViolations.push({ viewport: tag, url: url.slice(0, 300), reason: `cluster_id=${clusterId} != active scope` })
        }
      }
      // secret scan on same-origin JSON/text responses (cap size / count)
      if (url.startsWith(ENV.baseURL) && col.secretHits.length < 20 && status === 200) {
        const ct = (r.headers()['content-type'] || '')
        if (/json|text|javascript/.test(ct)) {
          try {
            const body = await r.text()
            if (body && body.length < 400000) {
              for (const p of SECRET_PATTERNS) {
                const m = body.match(p.re)
                if (m && m[0] !== ENV.password) {
                  col.secretHits.push({ viewport: tag, pattern: p.name, url: url.slice(0, 200), sample: m[0].slice(0, 12) + '***' })
                  break
                }
              }
            }
          } catch {}
        }
      }
    })
  }
  // loop detection over a window of the request log
  col.detectLoops = () => {
    const counts = {}
    for (const r of col.requests) {
      const key = `${r.method} ${r.url}`
      counts[key] = (counts[key] || 0) + 1
    }
    return Object.entries(counts)
      .filter(([, n]) => n > 20)
      .map(([url, n]) => ({ url, count: n }))
  }
  return col
}

function summarize(result) {
  const failed = (result.checks || []).filter((c) => !c.pass)
  const status = failed.length === 0 ? 'PASS' : 'FAIL'
  return {
    id: result.id,
    status,
    failedChecks: failed.map((c) => c.name),
    notes: result.notes || [],
  }
}

function writeResult(id, category, result) {
  ensureDirs()
  fs.mkdirSync(path.join(ROOT, category), { recursive: true })
  const summary = summarize(result)
  fs.writeFileSync(path.join(ROOT, category, `${id}.json`),
    JSON.stringify({ ...result, summary }, null, 2))
  return summary
}

function shot(page, name) {
  return page.screenshot({ path: path.join(ROOT, 'screenshots', `${name}.png`), fullPage: false }).catch(() => {})
}

// 会话封装。seedScope=true（默认）时对齐 harness.runPageTest 行为：
//  - addInitScript 在应用启动前写入 localStorage 'aiops-scope-preference'。
//    该值仅作为非权威偏好，活动授权 Scope 仍由 GET /me/POST /me/scope 决定；
//  - route 门闸挂起 /api/v1 数据请求，直到 UI 登录 + POST /me/scope 完成
//    （全新 UI 登录会重置服务端 active scope，避免挂载即 403 的爆发）。
// seedScope=false 供 PF-UI-002 使用：不预置、不门闸，走纯 UI 选择集群的真实流程。
async function withSession({ viewport, col, seedScope = true }, fn) {
  const browser = await launchBrowser()
  const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } })
  if (seedScope) {
    await context.addInitScript((clusterId) => {
      try { localStorage.setItem('aiops-scope-preference', JSON.stringify({ state: { preferredClusterId: clusterId }, version: 0 })) } catch {}
    }, ENV.clusterId)
  }
  let scopeReady = !seedScope
  if (seedScope) {
    await context.route(/\/api\/v1\//, async (r) => {
      if (/auth\/login|me\/scope/.test(r.request().url())) return r.continue()
      while (!scopeReady) await new Promise((res) => setTimeout(res, 100))
      await r.continue()
    })
  }
  const page = await context.newPage()
  col.attach(page)
  try {
    await uiLogin(page)
    if (seedScope) {
      // 全新 UI 登录会重置服务端会话 scope，需要重新建立（与 harness 一致）
      const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
        data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
      })
      if (!scopeResp.ok()) throw new Error(`scope set failed: ${scopeResp.status()}`)
      scopeReady = true
    }
    await fn(page)
  } finally {
    await context.close()
    await browser.close()
  }
}

module.exports = {
  ENV, ROOT, ensureDirs, GLOBAL_PATHS, SECRET_PATTERNS,
  uiLogin, spaNav, makeCollector, summarize, writeResult, shot, withSession,
}
