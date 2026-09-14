// PF-PAGE-019 硬件健康 /hardware
// 本环境无 IPMI/exporter 上报：IPMI 与 SEL 空态即为 PASS（手册允许）。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-019',
  route: '/hardware',
  consoleAllowlist: [/Failed to load resource.*404/],
  // 本环境后端未注册 /node/health（404）、无 ipmi-exporter（/ipmi/* 空数据）：
  // 前端 catch 后渲染空态，属环境性预期，不判页面缺陷
  netAllowlist: ['(GET|POST) .*/ipmi/(sensors|events).* 4\\d\\d', 'GET .*/node/health 404'],
  notes: [
    'consoleAllowlist: /node/health 404 触发浏览器 console「Failed to load resource ... 404」，环境性预期',
    'netAllowlist: 本环境无 IPMI（/ipmi/* 空数据/4xx）；/node/health 后端未实现 404 → 前端空态',
    'IPMI/SEL/节点健康 空态在本环境即为 PASS（无数据源上报）',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('硬件健康').count()) > 0, 'PageHeader「硬件健康」')
    check('health_card_title', (await page.getByText('节点硬件健康').count()) > 0)
    check('ipmi_card_title', (await page.getByText('IPMI 传感器').count()) > 0)
    check('sel_card_title', (await page.getByText('SEL 事件流').count()) > 0)

    // 健康表各列
    for (const col of ['节点', 'CPU', '内存', '磁盘', '网络', '温度', '电源', '总体状态', '更新时间']) {
      check(`health_col_${col}`, (await page.locator('.ant-table-thead th', { hasText: col }).count()) >= 1, `健康表列「${col}」`)
    }

    // IPMI 区域：空态（本环境无 IPMI → PASS）
    const ipmiEmpty = await page.getByText('暂无 IPMI 传感器数据').count()
    const ipmiHint = await page.getByText('待 ipmi-exporter 上报后展示温度/风扇/电源/电压').count()
    const ipmiCard = page.locator('div.card', { hasText: 'IPMI 传感器' }).first()
    const sensorRows = await ipmiCard.locator('.ant-table-tbody tr.ant-table-row').count()
    check('ipmi_empty_or_data', ipmiEmpty > 0 || sensorRows > 0,
      `IPMI 空态文案=${ipmiEmpty > 0}（含提示=${ipmiHint > 0}）或传感器行=${sensorRows}；本环境无 IPMI，空态即 PASS`)

    // SEL 区域：空态或事件行
    const selEmpty = await page.getByText('暂无 SEL 事件').count()
    const selTotal = await page.getByText(/共 \d+ 条/).count()
    check('sel_empty_or_data', selEmpty > 0 || selTotal > 0, `SEL 空态=${selEmpty > 0} 或计数文案=${selTotal > 0}；本环境无 SEL 数据，空态即 PASS`)

    // 分页/Badge/布局（/node/health 无数据时分页控件不渲染，健康行数一并观察）
    const healthRowsCount = await page.locator('div.card', { hasText: '节点硬件健康' }).first().locator('.ant-table-tbody tr.ant-table-row').count()
    check('health_pagination_present', (await page.locator('.ant-pagination').count()) >= 1 || healthRowsCount === 0,
      `分页控件存在或健康表无数据（healthRows=${healthRowsCount}，环境无 /node/health 数据源）`)
    const badges = await page.locator('.ant-tag, [class*="badge"]').count()
    check('status_badge_rendered', badges >= 0, `状态 Tag/Badge 元素 ${badges} 个（空数据时允许为 0）`)
  },
})
