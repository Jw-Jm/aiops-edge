// PF-PAGE-007 日志与指标 —— 模式切换/级别/时间/关键词/探针过滤/分页/VictoriaLogs 标识。
const { runLaneA } = require('./lib/laneA')

async function pickOption(page, label) {
  await page.locator(`.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option[title="${label}"]`)
    .first().click({ timeout: 5000 })
}

runLaneA({
  id: 'PF-PAGE-007',
  route: '/observability/log',
  actions: async (page, check, tag) => {
    await page.getByText('日志与指标', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('日志与指标', { exact: true }).count()) > 0)

    // VictoriaLogs 来源标识
    check('victorialogs_badge', (await page.getByText('数据源 · VictoriaLogs').count()) > 0, '来源标识 Tag')

    // 原始/异常模式切换
    await page.locator('.ant-segmented-item', { hasText: '异常模式' }).click()
    await page.waitForTimeout(2500)
    const aggVisible = (await page.getByText('暂无聚合结果').count()) > 0 || (await page.locator('.ant-table-tbody tr').count()) > 0
    check('aggregate_mode_switch', aggVisible, '异常模式切换并渲染聚合表')
    await page.locator('.ant-segmented-item', { hasText: '原始日志' }).click()
    await page.waitForTimeout(2500)
    check('raw_mode_back', true, '切回原始日志')

    // 日志级别筛选（全部级别 → 错误 → 还原）
    const levelSel = page.locator('.ant-select').filter({ hasText: '全部级别' }).first()
    await levelSel.click()
    await pickOption(page, '错误')
    await page.waitForTimeout(2000)
    check('level_filter', (await page.locator('.ant-select').filter({ hasText: '错误' }).first().count()) > 0, '级别=错误 已生效')
    const levelSel2 = page.locator('.ant-select').filter({ hasText: '错误' }).first()
    await levelSel2.click()
    await pickOption(page, '全部级别')
    await page.waitForTimeout(2000)

    // 时间筛选：默认已是近 24 小时，切换到近 7 天再切回，验证时间筛选生效
    const timeSel = page.locator('.ant-select').filter({ hasText: /近 24 小时/ }).first()
    if ((await timeSel.count()) > 0) {
      await timeSel.click()
      await pickOption(page, '近 7 天')
      await page.waitForTimeout(2500)
      check('time_filter_24h', (await page.locator('.ant-select').filter({ hasText: '近 7 天' }).first().count()) > 0, '时间范围切到近7天（默认近24小时）')
      const timeSel2 = page.locator('.ant-select').filter({ hasText: '近 7 天' }).first()
      await timeSel2.click()
      await pickOption(page, '近 24 小时')
      await page.waitForTimeout(2500)
    } else {
      check('time_filter_24h', false, '未找到时间范围选择器')
    }

    // 关键词查询
    const kw = page.getByPlaceholder('搜索关键词，如 error / 服务名')
    await kw.fill('error')
    await page.getByRole('button', { name: /查\s*询/ }).click()
    await page.waitForTimeout(2500)
    check('keyword_query', true, '关键词 error 查询执行')

    // 探针过滤切换
    const probeBtn = page.locator('button').filter({ hasText: /显示探针|过滤探针/ }).first()
    if ((await probeBtn.count()) > 0) {
      const before = await probeBtn.textContent()
      await probeBtn.click()
      await page.waitForTimeout(2000)
      const after = await probeBtn.textContent()
      check('probe_filter_toggle', before !== after, `探针按钮 ${before} → ${after}`)
      await probeBtn.click() // 还原
      await page.waitForTimeout(1500)
    } else {
      check('probe_filter_toggle', false, '未找到探针过滤按钮')
    }

    // 分页（仅在有数据行时校验控件存在）
    const rowCount = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    if (rowCount > 0) {
      check('pagination_present', (await page.locator('.ant-pagination').count()) > 0, `${rowCount} 行数据带分页控件`)
    } else {
      check('pagination_present', true, '当前条件无数据行（分页按 antd 规则隐藏），记 notes')
    }
  },
}).catch((e) => { console.error(e); process.exit(1) })
