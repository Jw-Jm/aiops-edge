// IA-GRAPH-001: typed resource search -> relation modes -> relation list.
// Graph data may legitimately be unavailable in a deployment; the lane still
// requires an explicit error/empty state and never accepts a white screen.
const { makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [{ width: 1440, height: 900 }, { width: 1280, height: 720 }, { width: 1024, height: 768 }]

async function run() {
  const result = { id: 'IA-GRAPH-001', route: '/resources?view=graph', category: 'flows', checks: [], notes: [] }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      await spaNav(page, '/resources?view=graph')
      await page.waitForTimeout(800)
      check('relation_page_visible', await page.getByRole('region', { name: '资源关系探索' }).isVisible().catch(() => false))
      check('typed_search_visible', await page.getByLabel('资源关系搜索').isVisible().catch(() => false))
      await page.getByLabel('资源关系搜索').fill('order')
      await page.getByRole('button', { name: '探索' }).click().catch(() => {})
      await page.waitForTimeout(900)
      const graphReady = await page.getByLabel('资源关系图').count().then((count) => count > 0).catch(() => false)
      const explicitState = await page.getByText(/开始关系探索|关系图读取失败|暂无/).count().then((count) => count > 0).catch(() => false)
      check('graph_or_explicit_state', graphReady || explicitState)
      if (graphReady) {
        check('mode_toolbar_visible', await page.getByLabel('关系图模式').count().then((count) => count > 0).catch(() => false))
        check('legend_visible', await page.getByText('故障传播').count().then((count) => count > 0).catch(() => false))
        check('relation_list_visible', await page.getByText('等价关系列表').count().then((count) => count > 0).catch(() => false))
      } else result.notes.push(`${tag}: 图谱数据不可用或无匹配资源，验证显式状态`)
      check('no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
      await shot(page, `graph--${tag}--default`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
