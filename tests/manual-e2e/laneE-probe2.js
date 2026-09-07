// One-off probe 2: fresh UI login, then call suspect endpoints from the browser
// context (cookie auth) and print statuses.
const { chromium } = require('playwright')
const fs = require('fs')
const ENV = JSON.parse(fs.readFileSync('tests/manual-e2e/env.json', 'utf8'))

;(async () => {
  const browser = await chromium.launch()
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await page.goto(ENV.baseURL + '/login', { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('input', { timeout: 15000 })
  for (let i = 0; i < 4; i++) {
    if (i > 0) await page.waitForTimeout(15000)
    await page.getByPlaceholder('用户名').fill(ENV.username)
    await page.getByPlaceholder('密码').fill(ENV.password)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    const ok = await page.waitForURL((u) => !String(u).includes('/login'), { timeout: 10000 }).then(() => true).catch(() => false)
    if (ok) break
  }
  await page.waitForTimeout(800)
  const eps = [
    ['GET', '/me'], ['GET', '/services'], ['GET', '/topology/global'],
    ['GET', '/system/components'], ['GET', '/ops/audit-logs?limit=2'],
    ['GET', '/settings/llm'], ['GET', '/settings/llm/providers'],
    ['GET', '/ai/actions?limit=3'], ['GET', '/infrastructure/deployments?namespace=all'],
    ['GET', '/ops/changes'], ['GET', '/clusters/1/nodes'],
    ['GET', '/dashboard/stats'], ['GET', '/traces?service=payments&limit=2'],
    ['GET', '/logs/query?service=payments&limit=2'], ['GET', '/users'],
  ]
  const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
    data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
  })
  console.log('scope set:', scopeResp.status())
  for (const [m, ep] of eps) {
    const r = await page.request.get(`${ENV.apiBase}${ep}`)
    const body = (await r.text()).slice(0, 120).replace(/\n/g, '')
    console.log(String(r.status()).padEnd(4), ep.padEnd(45), body)
  }
  await browser.close()
})()
