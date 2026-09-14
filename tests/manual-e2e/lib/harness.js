// Shared harness for the AIOps product-function manual (non-LLM phase).
// Usage: const { launchSession, finish } = require('./harness')
// Every run records: console messages, failed/4xx-5xx requests, screenshots at
// both mandated viewports, and a result JSON under test-results/<RUN_ID>/.
const fs = require('fs')
const path = require('path')
const { chromium } = require('playwright')

const ENV = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'env.json'), 'utf8'))
const RUN_ID = process.env.TEST_RUN_ID || 'nonllm-' + new Date().toISOString().replace(/[-:]/g, '').replace(/\..*/, 'Z')
const ROOT = path.resolve(__dirname, '..', '..', '..', 'test-results', RUN_ID)

function ensureDirs() {
  for (const d of ['pages', 'logic', 'page-data', 'ai-chain', 'backend', 'flows', 'screenshots', 'browser-console', 'browser-network', 'api', 'mysql', 'metrics', 'logs', 'traces', 'graph', 'k8s']) {
    fs.mkdirSync(path.join(ROOT, d), { recursive: true })
  }
}

// Login through the real API and persist cookie + scope into a storageState.
async function buildStorageState() {
  const browser = await chromium.launch()
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  const resp = await page.request.post(`${ENV.apiBase}/auth/login`, {
    data: { username: ENV.username, password: ENV.password },
  })
  if (!resp.ok()) throw new Error(`login failed: ${resp.status()} ${await resp.text()}`)
  const body = await resp.json()
  if (body.must_change_password) throw new Error('admin must change password first')
  const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
    data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
  })
  if (!scopeResp.ok()) throw new Error(`scope set failed: ${scopeResp.status()}`)
  const state = await ctx.storageState()
  await browser.close()
  return state
}

const STATE_CACHE = path.join(require('os').tmpdir(), 'aiops-e2e-storage-state.json')

let _state = null
async function getStorageState() {
  if (_state) return _state
  // Reuse a cached storageState across processes: the login endpoint is
  // rate-limited (429) and concurrent lanes share the same admin account.
  try {
    const cached = JSON.parse(fs.readFileSync(STATE_CACHE, 'utf8'))
    const probe = await chromium.launch()
    const p = await (await probe.newContext({ storageState: cached })).newPage()
    const ok = await p.request.get(`${ENV.apiBase}/me`).then((r) => r.ok()).catch(() => false)
    await probe.close()
    if (ok) { _state = cached; return _state }
  } catch { /* no cache yet */ }
  _state = await buildStorageState()
  try { fs.writeFileSync(STATE_CACHE, JSON.stringify(_state)) } catch {}
  return _state
}

// result: { id, route, notes: [], checks: [{name, pass, detail}] }
function summarize(result) {
  const failed = result.checks.filter((c) => !c.pass)
  const unexplainedConsole = result.consoleErrors.filter(
    (m) => !result.consoleAllowlist.some((re) => new RegExp(re).test(m.text))
  )
  const unexplainedNet = result.failedRequests.filter(
    (r) => !result.netAllowlist.some((re) => new RegExp(re).test(`${r.method} ${r.url} ${r.status}`))
  )
  const pass =
    failed.length === 0 &&
    unexplainedConsole.length === 0 &&
    unexplainedNet.length === 0 &&
    result.pageErrors.length === 0
  return {
    id: result.id,
    route: result.route,
    status: pass ? 'PASS' : 'FAIL',
    failedChecks: failed.map((c) => c.name),
    unexplainedConsole,
    unexplainedNetwork: unexplainedNet,
    pageErrors: result.pageErrors,
    notes: result.notes,
  }
}

// Frontend auth gate is an in-memory zustand flag set by the Login page
// (cookie alone is not enough on a fresh context). Detect the /login redirect,
// log in through the real UI, then re-apply scope on the new session cookie.
async function uiLoginAndScope(page) {
  await page.waitForSelector('input', { timeout: 10000 })
  const inputs = page.locator('input')
  await inputs.nth(0).fill(ENV.username)
  await inputs.nth(1).fill(ENV.password)
  await page.getByRole('button', { name: /登\s*录/ }).click()
  await page.waitForURL((u) => !String(u).includes('/login'), { timeout: 15000 }).catch(() => {})
  await page.waitForTimeout(500)
  const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
    data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
  })
  if (!scopeResp.ok()) throw new Error(`scope set failed: ${scopeResp.status()}`)
}

// Core page runner. actions(page, ctx) may perform page-specific interactions;
// each check pushed via ctx.check(name, pass, detail).
async function runPageTest({ id, route, actions, consoleAllowlist = [], netAllowlist = [], category = 'pages', notes = [], uiCluster = true, viewports = ENV.viewports }) {
  ensureDirs()
  const state = await getStorageState()
  const browser = await chromium.launch()
  const result = {
    id, route, category,
    consoleErrors: [], pageErrors: [], failedRequests: [],
    consoleAllowlist, netAllowlist,
    checks: [], notes: [...notes],
  }
  const ctxCheck = (name, pass, detail = '') => result.checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })

  for (const vp of viewports) {
    const tag = `${vp.width}x${vp.height}`
    const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, storageState: state })
    // The UI request interceptor takes the cluster scope from the in-memory
    // The active authorization scope is server-owned and hydrated from GET
    // /me. Seed only the non-authoritative cluster preference used by the
    // scope store; uiCluster=false leaves it unset (used to test the
    // "cluster not selected" restriction).
    if (uiCluster) {
      await context.addInitScript((clusterId) => {
        try { localStorage.setItem('aiops-scope-preference', JSON.stringify({ state: { preferredClusterId: clusterId }, version: 0 })) } catch {}
      }, ENV.clusterId)
    }
    const page = await context.newPage()
    page.on('console', (m) => {
      if (m.type() === 'error') result.consoleErrors.push({ viewport: tag, text: m.text().slice(0, 500) })
    })
    page.on('pageerror', (e) => result.pageErrors.push({ viewport: tag, text: String(e).slice(0, 500) }))
    page.on('response', (r) => {
      if (r.status() >= 400) result.failedRequests.push({ viewport: tag, method: r.request().method(), url: r.url(), status: r.status() })
    })
    try {
      await page.goto(`${ENV.baseURL}${route}`, { waitUntil: 'domcontentloaded', timeout: 30000 })
      await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => result.notes.push(`networkidle timeout @${tag}`))
      // The auth token lives in an in-memory zustand store (cookie carries the
      // real session), so a fresh page load on a protected route redirects to
      // /login. Perform the real UI login once, then SPA-navigate to the
      // target route (a hard reload would drop the in-memory token again).
      if (!route.startsWith('/login') && /\/login/.test(page.url())) {
        // Hold data API calls until the server-side scope is (re-)established:
        // a fresh UI login resets the session scope, and the App shell fires
        // data requests on mount which would otherwise 403 (SCOPE_SELECTION_REQUIRED).
        let scopeReady = false
        await context.route(/\/api\/v1\//, async (r) => {
          if (/auth\/login|me\/scope/.test(r.request().url())) return r.continue()
          while (!scopeReady) await new Promise((res) => setTimeout(res, 100))
          await r.continue()
        })
        // Login retry: POST /auth/login is rate-limited (10 / 60s / user+IP)
        // and parallel test lanes share the budget — back off and retry on 429.
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
        const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
          data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
        })
        if (!scopeResp.ok()) throw new Error(`scope set failed: ${scopeResp.status()}`)
        scopeReady = true
        await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
        await page.evaluate((u) => {
          window.history.pushState({}, '', u)
          window.dispatchEvent(new PopStateEvent('popstate'))
        }, route)
        await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
      }
      await page.waitForTimeout(800)
      if (actions) await actions(page, ctxCheck, tag)
      await page.screenshot({ path: path.join(ROOT, 'screenshots', `${id}-${tag}.png`), fullPage: false })
      ctxCheck(`viewport_${tag}_no_white_screen`, (await page.content()).length > 500)
    } catch (e) {
      result.pageErrors.push({ viewport: tag, text: `runner: ${String(e).slice(0, 500)}` })
      try { await page.screenshot({ path: path.join(ROOT, 'screenshots', `${id}-${tag}-error.png`) }) } catch {}
    }
    fs.writeFileSync(path.join(ROOT, 'browser-console', `${id}-${tag}.json`), JSON.stringify(result.consoleErrors.filter(c => c.viewport === tag), null, 2))
    fs.writeFileSync(path.join(ROOT, 'browser-network', `${id}-${tag}.json`), JSON.stringify(result.failedRequests.filter(r => r.viewport === tag), null, 2))
    await context.close()
  }
  await browser.close()
  const summary = summarize(result)
  fs.mkdirSync(path.join(ROOT, category), { recursive: true })
  fs.writeFileSync(path.join(ROOT, category, `${id}.json`), JSON.stringify({ ...result, summary }, null, 2))
  return summary
}

module.exports = { ENV, RUN_ID, ROOT, runPageTest, getStorageState, ensureDirs }
