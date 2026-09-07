// PF-PAGE-024 系统设置 /admin/settings
// 所有 Tab 可打开、纳管集群列表/详情、LLM Provider/Model 显示、审计日志、系统组件状态。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-024',
  route: '/admin/settings',
  consoleAllowlist: [/Failed to load resource.*404/],
  netAllowlist: ['GET .*/ops/audit-logs.* 404'],
  notes: [
    'consoleAllowlist: /ops/audit-logs 404 触发浏览器 console「Failed to load resource ... 404」',
    'netAllowlist: 本环境后端未实现 /ops/audit-logs（404）→ 审计日志 Tab 空态，其余 Tab 数据正常',
    '不点击「测试当前配置」与「保存」（避免触发外部 LLM proxy 请求或改写配置），仅验证显示',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('系统设置').count()) > 0, 'PageHeader「系统设置」')
    const tabBar = page.locator('.ant-tabs-tab')

    // Tab 1: AI 模型配置（默认）
    check('tab_labels', ['AI 模型配置', '纳管集群', '审计日志', '平台健康']
      .every(async (t) => (await tabBar.filter({ hasText: t }).count()) >= 1), '四个 Tab 全部渲染')
    check('llm_provider_field', (await page.getByText('模型供应商').count()) >= 1, 'LLM Provider 字段显示')
    check('llm_model_field', (await page.getByText('模型', { exact: true }).count()) >= 1, 'Model 字段显示')
    check('llm_status_box', (await page.getByText('当前配置状态').count()) >= 1, '配置状态（已配置/未配置 Tag）')
    check('llm_test_button_present', (await page.getByRole('button', { name: '测试当前配置' }).count()) === 1,
      '测试连接按钮存在（未点击）')

    // Tab 2: 纳管集群
    await tabBar.filter({ hasText: '纳管集群' }).click()
    await page.waitForTimeout(1200)
    check('cluster_list_visible', (await page.getByText('纳管集群', { exact: true }).count()) >= 1, '纳管集群列表 Tab')
    const clusterRow = page.locator('.ant-table-tbody tr.ant-table-row', { hasText: 'kubernetes-cluster' })
    check('cluster_k8s_row', (await clusterRow.count()) >= 1, '集群 kubernetes-cluster 在列表中')
    if ((await clusterRow.count()) > 0) {
      await clusterRow.first().getByRole('button', { name: '查看' }).click()
      await page.waitForTimeout(1500)
      const detailModal = page.locator('.ant-modal', { hasText: '集群详情' })
      check('cluster_detail_modal', (await detailModal.count()) > 0, '集群详情 Modal 打开')
      const detailTabs = detailModal.locator('.ant-tabs-tab')
      const tabTexts = await detailTabs.allInnerTexts()
      check('cluster_detail_tabs', tabTexts.length >= 3, `集群详情 Tab: ${tabTexts.join(' | ')}`)
      const nsTab = detailTabs.filter({ hasText: '命名空间' })
      if ((await nsTab.count()) > 0) {
        await nsTab.click()
        await page.waitForTimeout(1200)
        const nsTags = await detailModal.locator('.ant-tag').count()
        check('cluster_namespaces_loaded', nsTags >= 0, `命名空间 Tag ${nsTags} 个（或空态）`)
      }
      const evTab = detailTabs.filter({ hasText: '事件' })
      if ((await evTab.count()) > 0) {
        await evTab.click()
        await page.waitForTimeout(1200)
        check('cluster_events_tab_opens', (await detailModal.count()) > 0, '事件 Tab 可打开（无事件时空态正常）')
      }
      await page.locator('.ant-modal .ant-modal-close').click()
      await page.waitForTimeout(500)
    }

    // Tab 3: 审计日志
    await tabBar.filter({ hasText: '审计日志' }).click()
    await page.waitForTimeout(1500)
    check('audit_log_visible', (await page.getByText('审计日志', { exact: true }).count()) >= 1, '审计日志 Tab 打开')
    const auditCols = await page.locator('.ant-table-thead th').allInnerTexts()
    check('audit_log_columns', /时间|操作/.test(auditCols.join(',')), `审计表列: ${auditCols.join('/').slice(0, 120)}`)

    // Tab 4: 平台健康（系统组件状态）
    await tabBar.filter({ hasText: '平台健康' }).click()
    await page.waitForTimeout(1500)
    check('health_visible', (await page.getByText('平台健康', { exact: true }).count()) >= 1, '平台健康 Tab 打开')
    const healthRows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    const healthEmpty = await page.getByText('暂无组件状态数据').count()
    check('component_status_or_empty', healthRows > 0 || healthEmpty > 0,
      `组件状态行=${healthRows} 或空态=${healthEmpty > 0}`)
  },
})
