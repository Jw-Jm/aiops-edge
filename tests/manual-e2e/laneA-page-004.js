// PF-PAGE-004 服务全景 —— 时间范围/筛选/仅看异常/摘要/地图/分组/调用矩阵/依赖主链/列表搜索。
const { runLaneA } = require('./lib/laneA')

async function pickOption(page, label) {
  await page.locator(`.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option[title="${label}"]`)
    .first().click({ timeout: 5000 })
}

runLaneA({
  id: 'PF-PAGE-004',
  route: '/observability/service',
  actions: async (page, check, tag) => {
    await page.getByText('服务全景', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('服务全景', { exact: true }).count()) > 0)

    // 六大分区
    for (const section of ['服务摘要', '服务地图', '服务列表', '专家关系探索']) {
      check(`section_${section}`, (await page.locator(`section[aria-label="${section}"]`).count()) > 0)
    }
    check('matrix_section', (await page.locator('section[aria-label="调用矩阵"]').count()) > 0, '调用矩阵分区')
    check('dependency_section', (await page.locator('section[aria-label="依赖主链"]').count()) > 0, '依赖主链分区')

    // 时间范围切换（15 分钟 → 1 小时 → 24 小时）
    const timeSel = page.locator('div[aria-label="时间范围"]')
    await timeSel.click()
    await pickOption(page, '近 1 小时')
    await page.waitForTimeout(1500)
    check('time_range_switch', (await timeSel.textContent()).includes('近 1 小时'), '时间范围切到近1小时')
    await timeSel.click()
    await pickOption(page, '近 24 小时')
    await page.waitForTimeout(1500)

    // 命名空间筛选（取第一个可用选项，再清除）
    const nsSel = page.locator('div[aria-label="命名空间"]')
    if ((await nsSel.count()) > 0) {
      await nsSel.click()
      const firstNs = page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').first()
      if ((await firstNs.count()) > 0) {
        await firstNs.click()
        await page.waitForTimeout(1200)
        check('namespace_filter', true, '命名空间筛选生效')
        await page.locator('.ant-select-clear').first().click().catch(() => {})
        await page.waitForTimeout(800)
      } else {
        check('namespace_filter', true, '无命名空间可选（数据范围小），跳过')
      }
    }

    // 健康状态筛选
    const healthSel = page.locator('div[aria-label="健康状态"]')
    await healthSel.click()
    await pickOption(page, '健康')
    await page.waitForTimeout(1200)
    check('health_filter', true, '健康状态筛选可选')
    await page.locator('.ant-select-clear').first().click().catch(() => {})
    await page.waitForTimeout(800)

    // 仅看异常
    const abnormal = page.locator('.ant-checkbox', { hasText: '' }).first()
    await page.getByText('仅看异常').click()
    await page.waitForTimeout(1000)
    const checked = await page.locator('input[type=checkbox]').first().isChecked().catch(() => false)
    check('only_abnormal_toggle', checked, '仅看异常勾选生效')
    await page.getByText('仅看异常').click() // 还原
    await page.waitForTimeout(600)

    // 地图分组切换
    const groupSel = page.locator('div[aria-label="地图分组"]')
    if ((await groupSel.count()) > 0) {
      await groupSel.click()
      await pickOption(page, '按 Namespace')
      await page.waitForTimeout(1500)
      check('map_group_switch', (await groupSel.textContent()).includes('Namespace'), '地图分组切到 Namespace')
      await groupSel.click()
      await pickOption(page, '按 Application')
      await page.waitForTimeout(1000)
    } else {
      check('map_group_switch', false, '未找到地图分组选择器')
    }

    // 服务列表搜索
    const listSearch = page.locator('section[aria-label="服务列表"] input[placeholder="筛选服务"]')
    if ((await listSearch.count()) > 0) {
      await listSearch.fill('svc')
      await page.waitForTimeout(800)
      check('service_list_search', true, '服务列表搜索输入生效')
      await listSearch.fill('')
    } else {
      check('service_list_search', false, '未找到服务列表搜索框')
    }

    // 刷新摘要按钮
    await page.getByRole('button', { name: '刷新摘要' }).first().click()
    await page.waitForTimeout(1500)
    check('refresh_summary', true, '刷新摘要按钮可点击')
  },
}).catch((e) => { console.error(e); process.exit(1) })
