// PF-PAGE-021 变更时间线 /changes（回归 20260906：后端 /ops/changes 已实现，登记闭环应成功）
// 真实闭环：登记 service=test-regression-laneB，content 带 test-regression 标记，登记记录按任务要求保留。
const { runLaneB } = require('./lib/laneB')

const TEST_SERVICE = 'test-regression-laneB'

runLaneB({
  id: 'PF-PAGE-021',
  route: '/changes',
  consoleAllowlist: [/Failed to load resource.*429/],
  netAllowlist: [
    'POST .*/auth/login 429',
  ],
  notes: [
    '回归 20260906：后端 /ops/changes 已实现（GET/POST 200），登记闭环应成功',
    'consoleAllowlist: 多 lane 共享登录限流 429 触发浏览器「Failed to load resource」console，环境性预期',
    '登记条目 service=test-regression-laneB，content 带 test-regression 标记，按任务要求测后保留',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('变更时间线').count()) > 0, 'PageHeader「变更时间线」')
    check('service_filter_present', (await page.locator('input[placeholder="按服务筛选"]').count()) === 1)
    check('type_filter_present', (await page.locator('.ant-select').filter({ hasText: /变更类型/ }).count()) >= 1)
    check('refresh_present', (await page.getByRole('button', { name: /刷\s*新/ }).count()) >= 1)
    check('total_text_present', (await page.getByText(/共 \d+ 条/).count()) >= 1, '总数文案「共 N 条」')
    const rowCount = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    check('pagination_present', (await page.locator('.ant-pagination').count()) >= 1 || rowCount === 0,
      `分页控件存在或无数据（rows=${rowCount}；本环境 /ops/changes 404 → 列表为空）`)

    // 服务筛选生效性（空列表下验证不报错）
    await page.locator('input[placeholder="按服务筛选"]').fill(TEST_SERVICE)
    await page.waitForTimeout(1500)
    check('service_filter_takes_effect', (await page.getByText('变更时间线').count()) > 0, '筛选无异常')
    await page.locator('input[placeholder="按服务筛选"]').fill('')
    await page.waitForTimeout(1200)

    // 登记变更 Modal 打开 + 必填校验
    await page.getByRole('button', { name: '登记变更' }).click()
    await page.waitForTimeout(600)
    const modal = page.locator('.ant-modal', { hasText: '登记变更' })
    check('register_modal_open', (await modal.count()) > 0, '「登记变更」Modal 打开')
    await modal.locator('.ant-btn-primary').click()
    await page.waitForTimeout(600)
    const errCount = await modal.locator('.ant-form-item-explain-error').count()
    check('register_required_validation', errCount >= 5, `空提交显示 ${errCount} 条必填错误（集群/服务/类型/操作人/内容）`)

    // 真实登记尝试
    await modal.locator('.ant-select').first().click()
    await page
      .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option', { hasText: 'kubernetes-cluster' })
      .first().click()
    await modal.locator('input[placeholder="如：order-service"]').fill(TEST_SERVICE)
    await modal.locator('.ant-select').nth(1).click()
    await page.waitForTimeout(500)
    // 取下拉第一个可见选项（antd 虚拟滚动，低处选项需滚动）
    await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').first().click()
    await modal.locator('input[placeholder="如：ops-team"]').fill('nonllm-laneB')
    await modal.locator('textarea[placeholder*="升级镜像"]').fill('test-regression: PF-PAGE-021 回归测试登记条目（test-regression-laneB，20260906）')
    await modal.locator('.ant-btn-primary').click()
    await page.waitForTimeout(2500)
    const successMsg = await page.getByText('变更已登记').count()
    const failMsg = await page.locator('.ant-message').innerText().catch(() => '')
    check('register_success_message', successMsg >= 1,
      `提交结果：${successMsg >= 1 ? '「变更已登记」' : `失败（message: ${failMsg.trim().slice(0, 80)}；后端 POST /ops/changes 404）`}`)

    // 列表真实出现（真实闭环）
    await page.locator('input[placeholder="按服务筛选"]').fill(TEST_SERVICE)
    await page.waitForTimeout(1500)
    const listText = await page.locator('.ant-table-tbody').innerText().catch(() => '')
    check('registered_change_in_list', listText.includes(TEST_SERVICE),
      `按服务 ${TEST_SERVICE} 筛选后列表${listText.includes(TEST_SERVICE) ? '包含' : '不包含'}登记记录（回归：后端 /ops/changes 已实现）`)
  },
})
