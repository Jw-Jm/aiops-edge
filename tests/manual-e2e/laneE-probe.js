// One-off probe: load /admin/settings and /observability/relationships in a real
// browser context and record which API calls fail.
const { chromium } = require('playwright')
const fs = require('fs')
const ENV = JSON.parse(fs.readFileSync('tests/manual-e2e/env.json', 'utf8'))
const STATE = require('os').tmpdir() + '/aiops-e2e-storage-state.json'

;(async () => {
  let state
  try { state = JSON.parse(fs.readFileSync(STATE, 'utf8')) } catch { state = null }
  const browser = await chromium.launch()
  const ctx = await browser.newContext({ storageState: state })
  await ctx.addInitScript((cid) => {
    try { localStorage.setItem('aiops-ui-v3', JSON.stringify({ state: { collapsed: false, aiDockOpen: false, currentClusterId: cid }, version: 0 })) } catch {}
  }, ENV.clusterId)
  const page = await ctx.newPage()
  page.on('response', (r) => {
    if (r.url().includes('/api/') && r.status() >= 400)
      console.log('FAIL', r.status(), r.request().method(), r.url().replace(ENV.baseURL, ''))
  })
  // UI login if redirected
  await page.goto(ENV.baseURL + '/admin/settings', { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(2000)
  if (page.url().includes('/login')) {
    await page.getByPlaceholder('用户名').fill(ENV.username)
    await page.getByPlaceholder('密码').fill(ENV.password)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    await page.waitForURL((u) => !String(u).includes('/login'), { timeout: 15000 })
    await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId } })
    await page.evaluate((u) => { window.history.pushState({}, '', u); window.dispatchEvent(new PopStateEvent('popstate')) }, '/admin/settings')
    await page.waitForTimeout(3000)
  }
  console.log('--- settings page text sample:', (await page.locator('body').innerText()).slice(0, 200).replace(/\n/g, ' | '))
  await page.goto(ENV.baseURL + '/observability/relationships', { waitUntil: 'domcontentloaded' }).catch(() => {})
  await page.waitForTimeout(3000)
  console.log('--- relationships page text sample:', (await page.locator('body').innerText()).slice(0, 200).replace(/\n/g, ' | '))
  await browser.close()
})()
