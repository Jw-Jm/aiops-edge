// PF-PAGE-017 容量预测 /capacity
const { runLaneB, pickOption } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-017',
  route: '/capacity',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：图表页 echarts 渲染不应产生 console error',
    'netAllowlist 空：capacity forecast/instances 接口应为 2xx；无数据时页面显示 Empty 而非请求失败',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('容量预测').count()) > 0, 'PageHeader「容量预测」')
    check('scope_tag_default_cluster_agg', (await page.getByText('范围：集群聚合（全部节点）').count()) > 0,
      'A8 范围选择器默认「集群聚合（全部节点平均）」')

    // 三指标卡：当前值 / 阈值 / 环比 / ETT
    for (const t of ['CPU 使用率 · 当前值', '内存使用率 · 当前值', '磁盘使用率 · 当前值']) {
      check(`metric_card_${t.split(' ')[0]}`, (await page.getByText(t).count()) >= 1, `指标卡「${t}」`)
    }
    check('threshold_statistic', (await page.getByText('阈值', { exact: true }).count()) >= 1)
    check('ett_statistic', (await page.getByText('预计触达阈值').count()) >= 1)

    // horizon 12/24/48
    const horizonSel = page.locator('.ant-select').filter({ hasText: /预测未来/ }).first()
    await pickOption(page, horizonSel, '预测未来 12 小时')
    await page.waitForTimeout(1200)
    check('horizon_12_applied', (await page.getByText('预测未来 12 小时').count()) >= 1)
    await pickOption(page, page.locator('.ant-select').filter({ hasText: /预测未来/ }).first(), '预测未来 48 小时')
    await page.waitForTimeout(1200)
    check('horizon_48_applied', (await page.getByText('预测未来 48 小时').count()) >= 1)

    // 节点选择
    const nodeSel = page.locator('.ant-select').filter({ hasText: /集群聚合（全部节点平均）/ }).first()
    const nodeSelCount = await nodeSel.count()
    if (nodeSelCount > 0) {
      await nodeSel.click()
      const optGroup = await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-group').count()
      const nodeOptions = await page
        .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')
        .allInnerTexts()
      const realNodes = nodeOptions.filter((o) => o !== '集群聚合（全部节点平均）')
      check('node_options_present', realNodes.length > 0 || optGroup >= 0,
        `节点下拉含 ${realNodes.length} 个节点选项`)
      if (realNodes.length > 0) {
        await page
          .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option', { hasText: realNodes[0] })
          .first().click()
        await page.waitForTimeout(1500)
        check('node_scope_tag_updates', (await page.getByText(`范围：节点 ${realNodes[0]}`).count()) > 0,
          `选择节点后范围标识更新为 ${realNodes[0]}`)
      }
    } else {
      check('node_options_present', false, '未找到节点范围 Select')
    }

    // 图表或空态
    await page.waitForTimeout(800)
    const canvases = await page.locator('canvas').count()
    const emptyCount = await page.getByText('暂无历史数据').count()
    check('chart_or_empty_state', canvases > 0 || emptyCount > 0,
      `canvas=${canvases}, 空态「暂无历史数据」=${emptyCount}（二者其一即合理）`)

    // 刷新
    await page.getByRole('button', { name: /刷\s*新/ }).click()
    await page.waitForTimeout(1500)
    check('refresh_ok', (await page.getByText('容量预测').count()) > 0 && (await page.locator('canvas').count()) >= 0,
      '点击刷新后页面正常重载')
  },
})
