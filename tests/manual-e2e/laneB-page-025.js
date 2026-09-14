// PF-PAGE-025 图谱运维 /admin/graph-operations
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-025',
  route: '/admin/graph-operations',
  consoleAllowlist: [/Failed to load resource.*503/],
  netAllowlist: ['GET .*/ai/kg/ops/aliases.* 503'],
  notes: [
    'consoleAllowlist: aliases 503 触发浏览器 console「Failed to load resource ... 503」',
    'netAllowlist: /ai/kg/ops/aliases 返回 503 GRAPH_UNAVAILABLE（knowledge graph 后端不可用，curl 确认；sync/outbox/shadow 均 200）',
    '回归 20260906：GraphOpsPanel 已改为 allSettled 降级聚合——aliases 503 时其余 Tab（sync/outbox/shadow）正常渲染，aliases Tab 显示空列表，不再整页失效',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('图谱运维').count()) > 0, '页面标题「图谱运维」')
    const card = page.locator('.ant-card', { hasText: 'Graph Ops' })
    check('graph_ops_card', (await card.count()) > 0, 'Graph Ops Card 渲染')
    check('readonly_badge', (await card.getByText('只读审计视图').count()) > 0, '「只读审计视图」只读标识')

    // 四类数据 Tab（sync/outbox/aliases/shadow，含数量）
    await page.waitForTimeout(1500)
    const tabs = card.locator('.ant-tabs-tab')
    const tabTexts = (await tabs.allInnerTexts()).map((t) => t.replace(/\s+/g, ' ').trim())
    check('four_tabs_present', tabTexts.length === 4, `Tab 数量=${tabTexts.length}: ${tabTexts.join(' | ')}`)
    for (const key of ['sync', 'outbox', 'aliases', 'shadow']) {
      check(`tab_${key}`, tabTexts.some((t) => t.startsWith(key)), `Tab「${key} (N)」存在`)
    }

    // JSON 内容：<pre> 中为 JSON.stringify 输出（空数据为 []）
    const pre = card.locator('pre').first()
    const preText = await pre.innerText().catch(() => '')
    let jsonOk = false
    try { jsonOk = Array.isArray(JSON.parse(preText)) } catch { jsonOk = false }
    check('json_content_valid', preText.length > 0 && jsonOk,
      `当前 Tab <pre> 为合法 JSON 数组（${preText.slice(0, 80)}...，共 ${preText.length} 字符）`)

    // 切换其余 Tab 验证 JSON 均可渲染
    for (let i = 1; i < (await tabs.count()); i++) {
      await tabs.nth(i).click()
      await page.waitForTimeout(600)
    }
    const preAfter = await card.locator('pre').first().innerText().catch(() => '')
    check('all_tabs_render_json', preAfter.length > 0, '切换 Tab 后 JSON 内容区仍正常渲染')
  },
})
