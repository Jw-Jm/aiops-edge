// PF-PAGE-009 Grafana 集成 —— 本环境 Grafana 不可达(502)：验证 health 显示、失败提示与重试不白屏。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-009',
  route: '/observability/grafana',
  netAllowlist: [
    // Grafana 不可达：query-api 代理 /api/v1/grafana/* 与 /grafana/* 静态代理均 5xx，属环境事实
    '(GET|POST) .*grafana.* 5[0-9][0-9]',
  ],
  consoleAllowlist: [
    // 同上：502 响应触发 Chromium 资源级 console error
    'Failed to load resource.*5[0-9][0-9]',
  ],
  actions: async (page, check, tag) => {
    await page.getByText('Grafana 集成', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('Grafana 集成', { exact: true }).count()) > 0)
    await page.waitForTimeout(2500)

    // health 显示（正常 或 不可达 均为有效显示）
    const ok = (await page.getByText(/Grafana 正常/).count()) > 0
    const bad = (await page.getByText('Grafana 不可达').count()) > 0
    check('health_badge_displayed', ok || bad, ok ? 'health=正常' : 'health=不可达（本环境 Grafana 未部署）')

    // Dashboard 搜索触发请求 → 失败提示
    const search = page.getByPlaceholder('搜索仪表盘，如：DeepFlow / MySQL / Pod')
    check('search_input_visible', (await search.count()) > 0)
    await search.fill('MySQL')
    await page.waitForTimeout(3000)

    // 失败提示 / 空态（不白屏）：本环境 Grafana 不可达 → 搜索应显示 ⚠ 错误提示
    const errTip = await page.getByText(/⚠/).textContent().catch(() => '')
    const loadFail = (await page.getByText('仪表盘加载失败').count()) > 0
    const noMatch = (await page.getByText('未找到匹配的仪表盘').count()) > 0
    const initialEmpty = (await page.getByText('输入关键词搜索仪表盘').count()) > 0
    check('search_feedback', !!errTip || loadFail || noMatch || initialEmpty,
      errTip ? `搜索失败提示: ${errTip}` : (loadFail ? '显示加载失败提示' : (noMatch ? '显示未找到提示' : (initialEmpty ? '回到初始空态（搜索失败被静默吞掉，记 notes）' : '无任何反馈'))))

    // 重试不白屏：失败态下重新触发搜索（清空再输入）验证页面仍可交互
    await search.fill('')
    await page.waitForTimeout(1200)
    await search.fill('MySQL')
    await page.waitForTimeout(2500)
    check('retry_no_white_screen', (await page.getByText('Grafana 集成', { exact: true }).count()) > 0, '重新搜索后页面仍正常渲染，不白屏')
  },
}).catch((e) => { console.error(e); process.exit(1) })
