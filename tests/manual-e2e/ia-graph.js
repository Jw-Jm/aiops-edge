// IA-GRAPH-001: typed resource search -> relation modes -> relation list.
// The search term must come from AIOPS_E2E_GRAPH_QUERY: an unconfigured term,
// an unusable graph, or "no match" are FAIL, never an implicit PASS.
const { makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [{ width: 1440, height: 900 }, { width: 1280, height: 720 }, { width: 1024, height: 768 }]

async function run() {
  const result = { id: 'IA-GRAPH-001', route: '/resources?view=graph', category: 'flows', checks: [], notes: [] }
  const query = (process.env.AIOPS_E2E_GRAPH_QUERY || '').trim()
  if (!query) {
    result.checks.push({ name: 'graph_query_configured', pass: false, detail: 'AIOPS_E2E_GRAPH_QUERY is required' })
    const summary = writeResult(result.id, result.category, result)
    console.log(JSON.stringify(summary))
    process.exitCode = 1
    return summary
  }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      await spaNav(page, '/resources?view=graph')
      await page.waitForTimeout(300)
      check('relation_page_visible', await page.getByRole('region', { name: '资源关系探索' }).isVisible().catch(() => false))
      check('typed_search_visible', await page.getByLabel('资源关系搜索').isVisible().catch(() => false))
      await page.getByLabel('资源关系搜索').fill(query)
      await page.getByLabel('资源关系搜索').press('Enter')
      const candidate = page.locator('section[aria-label="资源关系探索"] button').filter({ hasText: query }).first()
      const candidateReady = await candidate.waitFor({ state: 'visible', timeoutMs: 12000 }).then(() => true).catch(() => false)
      if (candidateReady) await candidate.click()
      const graph = page.locator('[aria-label="资源关系图"]')
      const graphReady = candidateReady && await graph.waitFor({ state: 'visible', timeoutMs: 15000 }).then(() => true).catch(() => false)
      const explicitState = await page.getByText(/开始关系探索|关系图读取失败|暂无/).count().then((count) => count > 0).catch(() => false)
      // "无匹配" is an honest state for a real deployment, but it must be reported
      // as such instead of silently passing the gate.
      check('graph_or_explicit_state', graphReady || explicitState)
      check('graph_rendered_for_query', graphReady, `query=${query}`)
      if (graphReady) {
        check('mode_toolbar_visible', await page.getByLabel('关系图模式').count().then((count) => count > 0).catch(() => false))
        check('legend_visible', await page.getByText('故障传播').count().then((count) => count > 0).catch(() => false))
        check('relation_list_visible', await page.getByText('等价关系列表').count().then((count) => count > 0).catch(() => false))
        const relationRegion = page.getByRole('region', { name: '关系明细' })
        const relationRows = relationRegion.getByRole('button').filter({ hasText: /→/ })
        const relationTexts = await relationRows.allTextContents().catch(() => [])
        check('relation_direction_and_label_visible', relationTexts.length > 0 && relationTexts.some((text) => /拥有|依赖|暴露|连接|挂载|运行于|传播/.test(text)), `rows=${relationTexts.slice(0, 3).join(' | ')}`)
        check('canvas_rendered', await graph.locator('canvas').count() > 0)
      } else {
        result.notes.push(`${tag}: 图谱未渲染（query=${query}）；显式状态=${explicitState ? '可见' : '缺失'}`)
      }
      check('no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
      await shot(page, `graph--${tag}--default`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  if (result.checks.some((c) => !c.pass)) process.exitCode = 1
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
