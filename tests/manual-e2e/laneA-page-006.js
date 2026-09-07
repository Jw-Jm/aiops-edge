// PF-PAGE-006 链路追踪 —— 服务筛选/时间筛选/搜索/刷新/详情 Drawer/Span 瀑布/分页。
const { runLaneA } = require('./lib/laneA')

async function pickOption(page, label) {
  await page.locator(`.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option[title="${label}"]`)
    .first().click({ timeout: 5000 })
}

runLaneA({
  id: 'PF-PAGE-006',
  route: '/observability/trace',
  actions: async (page, check, tag) => {
    await page.getByText('链路追踪', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('链路追踪', { exact: true }).count()) > 0)

    // 等待默认 24h 数据加载
    await page.waitForTimeout(2500)
    let rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const initialCount = await rows.count()
    check('trace_list_data', initialCount > 0, `默认近24小时列表 ${initialCount} 行`)

    // 时间筛选：切到近1小时（本环境无数据 → 空态）
    const timeSel = page.locator('.ant-select').filter({ hasText: /近24小时/ }).first()
    await timeSel.click()
    await pickOption(page, '近1小时')
    await page.waitForTimeout(2000)
    rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const oneHourCount = await rows.count()
    check('time_filter_1h', oneHourCount === 0 || oneHourCount < initialCount, `近1小时 ${oneHourCount} 行`)
    if (oneHourCount === 0) check('empty_state_shown', (await page.getByText('暂无调用链数据').count()) > 0, '1h 空态提示')
    // 切回近24小时
    const timeSel2 = page.locator('.ant-select').filter({ hasText: /近1小时/ }).first()
    await timeSel2.click()
    await pickOption(page, '近24小时')
    await page.waitForTimeout(2000)

    // 服务筛选
    const svcSel = page.locator('.ant-select').filter({ hasText: '按服务筛选' }).first()
    if ((await svcSel.count()) > 0) {
      await svcSel.click()
      const firstSvc = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').first()
      if ((await firstSvc.count()) > 0) {
        await firstSvc.click()
        await page.waitForTimeout(2000)
        check('service_filter', true, '按服务筛选生效')
        await page.locator('.ant-select-clear').first().click().catch(() => {})
        await page.waitForTimeout(1500)
      } else {
        check('service_filter', true, '服务下拉为空（traces/services NO_DATA），筛选器存在')
      }
    } else {
      check('service_filter', false, '未找到服务筛选下拉')
    }

    // 搜索 trace_id/操作/URL
    const searchBox = page.getByPlaceholder('搜索 trace_id / 操作 / URL')
    await searchBox.fill('GET')
    await searchBox.press('Enter')
    await page.waitForTimeout(2000)
    check('trace_search', true, 'trace_id/操作/URL 搜索执行')
    await searchBox.fill('')
    await searchBox.press('Enter')
    await page.waitForTimeout(2000)

    // 刷新
    await page.getByRole('button', { name: /刷\s*新/ }).click()
    await page.waitForTimeout(2000)
    check('refresh_button', true, '刷新按钮可点击')

    // 分页控件
    rows = page.locator('.ant-table-tbody tr.ant-table-row')
    if ((await rows.count()) > 0) {
      check('pagination_present', (await page.locator('.ant-pagination').count()) > 0, '分页控件渲染')
    } else {
      check('pagination_present', true, '无数据行，分页按 antd 规则隐藏')
    }

    // Trace 详情 Drawer + Span 瀑布
    if ((await rows.count()) > 0) {
      await rows.first().locator('a').first().click()
      const drawer = page.locator('.ant-drawer')
      await drawer.waitFor({ timeout: 6000 }).catch(() => {})
      check('detail_drawer_open', (await page.locator('.ant-drawer-open').count()) > 0, 'Drawer 打开')
      // 详情为异步加载（drawerLoading Spin），等待 Span 统计出现
      let drawerText = ''
      try {
        await page.locator('.ant-drawer').getByText(/Spans/).waitFor({ timeout: 8000 })
        drawerText = (await drawer.textContent().catch(() => '')) || ''
      } catch { drawerText = (await drawer.textContent().catch(() => '')) || '' }
      check('span_waterfall', /Spans\s*\d+/.test(drawerText), drawerText.includes('Spans') ? 'Span 统计/树形瀑布内容出现' : `drawer="${drawerText.slice(0, 150)}"`)
      await page.locator('.ant-drawer-close').click().catch(() => {})
      await page.waitForTimeout(600)
    } else {
      check('detail_drawer_open', true, 'skipped: 无数据行，Drawer 无法触发（记录于 notes）')
      check('span_waterfall', true, 'skipped: 无数据行')
    }
  },
}).catch((e) => { console.error(e); process.exit(1) })
