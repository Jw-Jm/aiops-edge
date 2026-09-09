// IA-WORKBENCH-001: workbench issue queue -> explicit investigation draft.
// This lane is intentionally data-aware: an empty production scope is a
// valid state and is reported as such, while any available issue must expose
// the two-click path into the draft page.
const { ENV, makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 1280, height: 720 },
  { width: 1024, height: 768 },
]

async function run() {
  const result = { id: 'IA-WORKBENCH-001', route: '/overview', category: 'flows', checks: [], notes: [] }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      await spaNav(page, '/overview')
      await page.waitForTimeout(1000)
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      check('no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
      check('issue_queue_visible', await page.getByTestId('issue-queue').isVisible().catch(() => false))
      check('production_scope_bar_visible', await page.locator('.scope-bar').getByText('生产平台', { exact: true }).count().then((count) => count > 0).catch(() => false))
      check('no_legacy_environment_selector', !(await page.locator('body').innerText()).match(/开发环境|测试环境|staging|dev/i))
      const startButton = page.getByRole('button', { name: '开始调查' }).first()
      if (await startButton.count()) {
        await startButton.click()
        await page.waitForTimeout(300)
        check('navigates_to_investigation_draft', /\/investigation\/new/.test(page.url()))
        check('symptom_prefilled', await page.getByLabel('症状 / 调查目标').inputValue().then((value) => Boolean(value)).catch(() => false))
      } else {
        check('empty_scope_is_explicit', await page.getByText('当前作用域暂无活跃问题').isVisible().catch(() => false))
        result.notes.push(`${tag}: 当前作用域没有活跃问题，未伪造调查入口数据`)
      }
      await shot(page, `overview--${tag}--default`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
