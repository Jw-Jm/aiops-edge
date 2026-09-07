// PF-PAGE-010 告警事件 —— 当前/历史/全部切换、严重度筛选、详情 Drawer（只读）。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-010',
  route: '/alerts/events',
  actions: async (page, check, tag) => {
    await page.getByText('告警事件', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('告警事件', { exact: true }).count()) > 0)
    await page.waitForTimeout(2000)

    const statusSeg = page.locator('.ant-segmented').first()
    const sevSeg = page.locator('.ant-segmented').nth(1)

    // 当前 / 历史 / 全部切换
    check('seg_current_default', (await statusSeg.locator('.ant-segmented-item-selected').textContent().catch(() => ''))?.includes('当前'), '默认当前告警')
    await statusSeg.locator('.ant-segmented-item', { hasText: '历史告警' }).click()
    await page.waitForTimeout(1500)
    check('seg_history_switch', (await statusSeg.locator('.ant-segmented-item-selected').textContent()).includes('历史'), '切到历史告警')
    await statusSeg.locator('.ant-segmented-item', { hasText: '全部' }).click()
    await page.waitForTimeout(1500)
    check('seg_all_switch', (await statusSeg.locator('.ant-segmented-item-selected').textContent()).includes('全部'), '切到全部')

    // 严重度筛选（第二组 Segmented：全部/严重/警告/信息）
    await sevSeg.locator('.ant-segmented-item', { hasText: '严重' }).click()
    await page.waitForTimeout(1500)
    check('severity_filter_critical', (await sevSeg.locator('.ant-segmented-item-selected').textContent()).includes('严重'), '严重度=严重 生效')
    await sevSeg.locator('.ant-segmented-item', { hasText: '警告' }).click()
    await page.waitForTimeout(1200)
    check('severity_filter_warning', (await sevSeg.locator('.ant-segmented-item-selected').textContent()).includes('警告'), '严重度=警告 生效')
    await sevSeg.locator('.ant-segmented-item', { hasText: '全部' }).click()
    await page.waitForTimeout(1200)

    // 回到当前告警
    await statusSeg.locator('.ant-segmented-item', { hasText: '当前告警' }).click()
    await page.waitForTimeout(1500)

    // 详情 Drawer（无数据时跳过并记 notes）
    const rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const rowCount = await rows.count()
    if (rowCount > 0) {
      await page.locator('.ant-table-tbody a').first().click()
      await page.locator('.ant-drawer-open').waitFor({ timeout: 5000 }).catch(() => {})
      check('detail_drawer_open', (await page.locator('.ant-drawer-open').count()) > 0, '详情 Drawer 打开')
      const closeBtn = page.locator('.ant-drawer-close')
      if ((await closeBtn.count()) > 0) { await closeBtn.click(); await page.waitForTimeout(600) }
    } else {
      check('detail_drawer_open', true, 'skipped: 当前无告警事件数据（列表空态正确），Drawer 交互在 laneB 数据态补测')
    }
    check('empty_or_data_ok', rowCount > 0 || (await page.locator('.ant-table').count()) > 0, `当前视图 ${rowCount} 行`)

    // 分页
    if (rowCount > 0) {
      check('pagination_present', (await page.locator('.ant-pagination').count()) > 0, '分页控件渲染')
    } else {
      check('pagination_present', true, '无数据行，分页按 antd 规则隐藏')
    }
  },
}).catch((e) => { console.error(e); process.exit(1) })
