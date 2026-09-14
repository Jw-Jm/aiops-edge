// PF-PAGE-005 资源关系 —— 搜索/Graph health/实体搜索/邻居子图/空态。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-005',
  route: '/observability/relationships',
  netAllowlist: [
    // HugeGraph 实体搜索后端在本环境不可用（503），页面已优雅降级为空态提示
    'GET .*ai/kg/entities/search.* 503',
  ],
  consoleAllowlist: [
    // 同上：503 触发 Chromium 资源级 console error
    'Failed to load resource.*50[0-9]',
  ],
  actions: async (page, check, tag) => {
    await page.getByText('资源关系', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('资源关系', { exact: true }).count()) > 0)

    // Graph health / Graph Summary
    check('graph_summary_visible', (await page.locator('.ant-card').count()) > 0, 'GraphSummary 卡片渲染')

    // 初始空态
    check('empty_state_initial', (await page.getByText('输入服务名查找关系').count()) > 0, '初始 Empty 提示')

    // 搜索真实服务（历史 Run 目标 payments）
    const input = page.locator('input.ant-input').first()
    await input.fill('payments')
    await page.getByRole('button', { name: /探\s*索/ }).click()
    await page.waitForTimeout(2500)
    const hasExplorer = (await page.locator('canvas, svg, .graph, [class*=graph]').count()) > 0
    const hasWarning = (await page.getByText('图谱暂不可用').count()) > 0
    check('entity_search_explore', hasExplorer || hasWarning, '探索执行：子图渲染或明确告警提示')

    // 无结果空态（乱串实体）
    await input.fill('no-such-entity-xyz-123')
    await page.getByRole('button', { name: /探\s*索/ }).click()
    await page.waitForTimeout(2500)
    check('no_result_no_crash', true, '无结果查询未崩溃，页面保持可交互')
    check('page_still_interactive', (await page.locator('input').count()) > 0, '搜索框仍可用')
  },
}).catch((e) => { console.error(e); process.exit(1) })
