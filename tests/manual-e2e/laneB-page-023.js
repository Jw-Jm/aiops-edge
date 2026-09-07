// PF-PAGE-023 用户管理 /admin/users
// 不实际创建/删除用户（完整生命周期由后续 lane 执行）；仅 Modal 打开 + 校验 + 角色选择。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-023',
  route: '/admin/users',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：列表 + Modal 页不应产生 console error',
    'netAllowlist 空：/users 列表为只读 2xx',
    '按测试约束不实际创建/删除用户：新增 Modal 只做空提交校验与角色下拉验证后关闭',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('用户管理').count()) > 0, 'PageHeader「用户管理」')
    for (const col of ['用户名', '显示名', '角色', '邮箱', '状态', '操作']) {
      check(`user_col_${col}`, (await page.locator('.ant-table-thead th', { hasText: col }).count()) >= 1, `列「${col}」`)
    }
    const rows = await page.locator('.ant-table-tbody tr.ant-table-row').count()
    check('user_list_loaded', rows >= 1, `用户列表 ${rows} 行（admin 至少存在）`)
    check('admin_row_present', (await page.locator('.ant-table-tbody', { hasText: 'admin' }).count()) >= 1)
    check('pagination_present', (await page.locator('.ant-pagination').count()) >= 1)

    // 搜索：命中 / 不命中
    const searchBox = page.locator('input[placeholder="按用户名 / 显示名 / 邮箱搜索"]')
    await searchBox.fill('admin')
    await page.waitForTimeout(800)
    check('search_hit', (await page.locator('.ant-table-tbody tr.ant-table-row').count()) >= 1, '搜索 admin 命中')
    await searchBox.fill('zz-no-such-user-laneB')
    await page.waitForTimeout(800)
    check('search_miss_empty_state', (await page.getByText('暂无用户').count()) > 0, '无命中显示「暂无用户」空态')
    await searchBox.fill('')
    await page.waitForTimeout(600)

    // 新增用户 Modal + 校验 + 角色选择
    await page.getByRole('button', { name: '新增用户' }).click()
    await page.waitForTimeout(600)
    const modal = page.locator('.ant-modal', { hasText: '新增用户' })
    check('create_modal_open', (await modal.count()) > 0, '「新增用户」Modal 打开')
    await modal.locator('.ant-btn-primary').click()
    await page.waitForTimeout(600)
    const errCount = await modal.locator('.ant-form-item-explain-error').count()
    check('create_required_validation', errCount >= 3, `空提交显示 ${errCount} 条必填错误（用户名/密码/邮箱）`)
    check('role_select_default_user', (await modal.locator('.ant-select-selection-item[title="普通用户"]').count()) >= 1,
      '角色默认「普通用户」')
    await modal.locator('.ant-select').click()
    await page.waitForTimeout(500)
    const roleOptions = await page
      .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')
      .allInnerTexts()
    check('role_options_complete',
      ['系统管理员', '审批人', '普通用户'].every((r) => roleOptions.some((o) => o.includes(r))),
      `角色下拉选项: ${roleOptions.join('/')}`)
    await page.keyboard.press('Escape')
    await page.waitForTimeout(300)
    await modal.locator('.ant-btn:not(.ant-btn-primary)').last().click()
    await page.waitForTimeout(500)
    check('modal_closed_without_create', (await page.locator('.ant-modal-wrap:not(.ant-modal-wrap-hidden)').count()) === 0,
      '取消关闭 Modal，未创建用户')
  },
})
