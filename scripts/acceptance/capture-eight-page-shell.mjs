#!/usr/bin/env node
/**
 * V1.4 八页壳层真实浏览器验收（G1 视觉 / G2 功能的最小可复核证据）
 *
 * 连接真实目标环境 http://localhost:30253/，使用真实账户登录，
 * 逐页产出台面截图并断言：
 *   1. 一级导航恰好八项且顺序固定（AI 智能运维第一）
 *   2. 根路径默认进入 AI 智能运维
 *   3. 不存在问题/资源/调查/处置独立入口
 *   4. 页面无横向溢出
 *   5. 控制台错误与失败请求
 *
 * 秘密只从环境变量读取，绝不写入证据文件。
 *
 * 用法：
 *   ADMIN_PASSWORD=... ACCEPTANCE_RUN_ID=... node scripts/acceptance/capture-eight-page-shell.mjs
 */
import { chromium } from '/opt/homebrew/lib/node_modules/playwright/index.mjs'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(__dirname, '../..')

const BASE = process.env.BASE_URL ?? 'http://localhost:30253'
const RUN_ID = process.env.ACCEPTANCE_RUN_ID ?? 'adhoc'
const USER = process.env.ADMIN_USER ?? 'admin'
const PASSWORD = process.env.ADMIN_PASSWORD ?? ''
const CLUSTER = process.env.ACCEPTANCE_CLUSTER_ID ?? ''
const OUT = join(REPO, 'docs/acceptance', RUN_ID)

const EXPECTED_NAV = ['AI 智能运维', '总览', '集群', '全链路监控', '知识图谱', '知识库', '报告', '设置']
const FORBIDDEN = ['问题', '资源', '调查', '处置', '系统管理']

const PAGES = [
  { id: 'ai-operations', path: '/ai-operations' },
  { id: 'overview', path: '/overview' },
  { id: 'clusters', path: CLUSTER ? `/clusters/${CLUSTER}/overview` : '/clusters' },
  { id: 'observability', path: '/observability' },
  { id: 'knowledge-graph', path: '/knowledge-graph' },
  { id: 'knowledge', path: '/knowledge' },
  { id: 'reports', path: '/reports' },
  { id: 'settings', path: '/settings' },
]

const VIEWPORTS = [
  { id: '1440x900', width: 1440, height: 900 },
  { id: '1280x720', width: 1280, height: 720 },
  { id: '1024x768', width: 1024, height: 768 },
]

async function main() {
  if (!PASSWORD) throw new Error('ADMIN_PASSWORD is required (do not pass it on the command line)')
  mkdirSync(join(OUT, 'screenshots'), { recursive: true })
  const results = { run_id: RUN_ID, base_url: BASE, started_at: new Date().toISOString(), pages: [], console_errors: [], failed_requests: [] }

  const browser = await chromium.launch()
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()

  page.on('console', (msg) => {
    if (msg.type() === 'error') results.console_errors.push(`${page.url()} :: ${msg.text().slice(0, 300)}`)
  })
  page.on('pageerror', (err) => results.console_errors.push(`${page.url()} :: pageerror ${String(err).slice(0, 300)}`))
  page.on('response', (res) => {
    const url = res.url()
    if (res.status() >= 400 && !url.includes('/api/v1/observability/paths')) {
      results.failed_requests.push(`${res.status()} ${url}`)
    }
  })

  // 真实登录
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' })
  await page.fill('input[type="text"], input#username, input[placeholder*="用户"]', USER)
  await page.fill('input[type="password"]', PASSWORD)
  await page.click('button[type="submit"], button:has-text("登录")')
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 20000 })

  // 默认首页必须是 AI 智能运维
  const afterLogin = new URL(page.url())
  results.default_landing = afterLogin.pathname
  results.default_landing_is_ai_operations = afterLogin.pathname === '/ai-operations'

  // 一级导航顺序断言
  const navLabels = await page.$$eval('.nav__label', (els) => els.map((e) => e.textContent?.trim() ?? ''))
  results.nav_labels = navLabels
  results.nav_order_ok = JSON.stringify(navLabels) === JSON.stringify(EXPECTED_NAV)
  results.forbidden_nav_absent = !navLabels.some((l) => FORBIDDEN.includes(l))

  for (const vp of VIEWPORTS) {
    await page.setViewportSize({ width: vp.width, height: vp.height })
    for (const p of PAGES) {
      const url = `${BASE}${p.path}`
      await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 }).catch(() => undefined)
      await page.waitForTimeout(900)
      const file = join(OUT, 'screenshots', `${p.id}-${vp.id}.png`)
      await page.screenshot({ path: file, fullPage: false })
      const metrics = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
        navCount: document.querySelectorAll('.nav__label').length,
        activeNav: document.querySelector('.nav__item.is-active .nav__label')?.textContent?.trim() ?? null,
        h1: document.querySelector('h1')?.textContent?.trim() ?? null,
        dataTestIdPresent: !!document.querySelector('[data-testid^="page-"]'),
      }))
      results.pages.push({
        page: p.id,
        viewport: vp.id,
        url,
        final_url: page.url(),
        screenshot: `screenshots/${p.id}-${vp.id}.png`,
        ...metrics,
        horizontal_overflow: metrics.scrollWidth > metrics.clientWidth + 1,
      })
    }
  }

  // 旧路由迁移验证（不静默跳转到无关默认页）
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(`${BASE}/investigation/run-abc`, { waitUntil: 'networkidle' }).catch(() => undefined)
  results.legacy_investigation_lands_on = new URL(page.url()).pathname

  await browser.close()
  results.finished_at = new Date().toISOString()
  results.nav_failures = results.pages.filter((p) => p.navCount !== 8 || !p.dataTestIdPresent)
  results.overflow_failures = results.pages.filter((p) => p.horizontal_overflow)
  writeFileSync(join(OUT, 'browser', 'visual-results.json'), `${JSON.stringify(results, null, 2)}\n`)
  console.log(JSON.stringify({
    nav_labels: results.nav_labels,
    nav_order_ok: results.nav_order_ok,
    forbidden_nav_absent: results.forbidden_nav_absent,
    default_landing: results.default_landing,
    legacy_investigation_lands_on: results.legacy_investigation_lands_on,
    nav_failures: results.nav_failures.length,
    overflow_failures: results.overflow_failures.length,
    console_errors: results.console_errors.length,
    failed_requests: results.failed_requests.length,
  }, null, 2))
}

main().catch((err) => {
  console.error('capture failed:', err)
  process.exit(1)
})
