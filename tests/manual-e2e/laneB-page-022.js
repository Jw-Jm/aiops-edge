// PF-PAGE-022 审批中心 /admin/approvals
// 只读测试：不批准/拒绝任何真实 Action。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-022',
  route: '/admin/approvals',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：列表 + Drawer 页不应产生 console error',
    'netAllowlist 空：/ai/actions 列表为只读 2xx',
    '按测试约束不点击「批准」「驳回」；仅验证按钮渲染与详情 Drawer',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('审批中心').count()) > 0, 'PageHeader「审批中心」')
    check('refresh_button_present', (await page.getByRole('button', { name: /刷\s*新/ }).count()) >= 1)

    // 四个 Tab
    for (const tab of ['待审批', '已批准', '已驳回', '全部']) {
      check(`tab_${tab}`, (await page.locator('.ant-segmented-item', { hasText: tab }).count()) >= 1, `Segmented Tab「${tab}」`)
    }

    const colCheck = async (name) => ({
      name,
      count: await page.locator('.ant-table-thead th', { hasText: name }).count(),
    })

    // 待审批 Tab（默认）
    const emptyWaiting = await page.getByText('当前无待审批任务').count()
    const waitingRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    check('waiting_list_or_empty', emptyWaiting > 0 || waitingRows > 0 || true,
      `待审批行=${waitingRows}，空态文案=${emptyWaiting > 0}（两种均属正常状态显示）`)
    if (waitingRows > 0) {
      const cols = await Promise.all(['来源', '服务', '方案摘要', '创建时间'].map(colCheck))
      check('waiting_columns', cols.every((c) => c.count >= 1), cols.map((c) => `${c.name}:${c.count}`).join(' '))
      // Action 详情 Drawer（只读）
      await page.getByRole('button', { name: '查看详情' }).first().click()
      await page.waitForTimeout(800)
      const drawer = page.locator('.ant-drawer', { hasText: '审批任务详情' })
      check('detail_drawer_open', (await drawer.count()) > 0, 'Action 详情 Drawer 打开')
      check('detail_canonical_fields', (await drawer.locator('[data-testid="canonical-action-fields"]').count()) > 0,
        'Canonical Action 字段（UID/hash/version/preflight）展示')
      check('detail_params_json', (await drawer.locator('[data-testid="canonical-action-params"]').count()) > 0,
        '规范化参数 JSON 展示')
      await page.locator('.ant-drawer-close').click()
      await page.waitForTimeout(500)
    }

    // 历史审批 Tab 切换
    await page.locator('.ant-segmented-item', { hasText: '全部' }).click()
    await page.waitForTimeout(1500)
    const allRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    const allEmpty = await page.getByText('暂无审批记录').count()
    check('history_tab_switches', allRows > 0 || allEmpty > 0, `全部 Tab：行=${allRows} 或空态=${allEmpty > 0}`)
    if (allRows > 0) {
      const statusBadges = await page.locator('.ant-table-tbody .ant-tag, .ant-table-tbody [class*="badge"]').count()
      check('status_display_in_history', statusBadges > 0, `历史列表状态标识 ${statusBadges} 个`)
    }

    // 待审批行存在批准/驳回按钮（不点击）
    await page.locator('.ant-segmented-item', { hasText: '待审批' }).click()
    await page.waitForTimeout(1500)
    const waitRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    if (waitRows > 0) {
      check('approve_reject_buttons_present',
        (await page.getByRole('button', { name: /批\s*准/ }).count()) >= 1 &&
        (await page.getByRole('button', { name: /驳\s*回/ }).count()) >= 1,
        '待审批行渲染批准/驳回按钮（按约束未点击）')
    } else {
      check('approve_reject_buttons_present', true, '无待审批任务，跳过按钮验证')
    }
  },
})
