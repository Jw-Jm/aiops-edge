// PF-PAGE-011 告警规则 —— 列表、新建 Modal + 必填校验（不保存）、类型动态字段、详情/编辑入口。
const { runLaneA } = require('./lib/laneA')

async function pickOption(page, label) {
  await page.locator(`.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option[title="${label}"]`)
    .first().click({ timeout: 5000 })
}

runLaneA({
  id: 'PF-PAGE-011',
  route: '/alerts/rules',
  actions: async (page, check, tag) => {
    await page.getByText('告警规则', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('告警规则', { exact: true }).count()) > 0)
    await page.waitForTimeout(2000)

    // 规则列表
    const rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const rowCount = await rows.count()
    check('rule_list_data', rowCount > 0, `规则列表 ${rowCount} 行`)

    // 新建 Modal 打开
    await page.getByRole('button', { name: '新建规则' }).click()
    await page.locator('.ant-modal:visible').waitFor({ timeout: 5000 }).catch(() => {})
    check('create_modal_open', (await page.getByText('新建告警规则').count()) > 0, '新建 Modal 打开')

    // 必填校验：不填直接点确定（不保存）
    await page.getByRole('button', { name: /确\s*定/ }).click()
    await page.waitForTimeout(1000)
    const errCount = await page.locator('.ant-form-item-explain-error').count()
    check('required_validation', errCount > 0, `必填校验错误数 ${errCount}，规则未被创建`)

    // 类型切换动态字段：异常检测(zscore) → 检测方法
    const typeSel = page.locator('.ant-modal .ant-form-item', { hasText: '规则类型' }).locator('.ant-select').first()
    if ((await typeSel.count()) > 0) {
      await typeSel.click()
      await pickOption(page, '异常检测(zscore)')
      await page.waitForTimeout(800)
      check('type_anomaly_dynamic_field', (await page.locator('.ant-modal label', { hasText: '检测方法' }).count()) > 0, 'anomaly 出现检测方法字段')

      await typeSel.click()
      await pickOption(page, 'SLO 烧毁率')
      await page.waitForTimeout(800)
      check('type_burn_rate_dynamic_field', (await page.locator('.ant-modal label', { hasText: '关联 SLO ID' }).count()) > 0, 'burn_rate 出现关联 SLO ID 字段')

      await typeSel.click()
      await pickOption(page, '日志关键字')
      await page.waitForTimeout(800)
      check('type_log_dynamic_field', (await page.locator('.ant-modal label', { hasText: '日志关键字' }).count()) > 0, 'log 出现关键字字段')
    } else {
      check('type_anomaly_dynamic_field', false, '未找到规则类型选择器')
      check('type_burn_rate_dynamic_field', false, '')
      check('type_log_dynamic_field', false, '')
    }

    // 关闭 Modal，绝不保存
    await page.getByRole('button', { name: /取\s*消/ }).click()
    await page.waitForTimeout(800)
    check('create_modal_closed_no_save', (await page.getByText('新建告警规则').count()) === 0, 'Modal 已取消，未创建规则')

    // 详情 Drawer + 编辑入口
    if (rowCount > 0) {
      await page.getByRole('button', { name: /详\s*情/ }).first().click()
      await page.locator('.ant-drawer-open').waitFor({ timeout: 5000 }).catch(() => {})
      check('detail_drawer_open', (await page.getByText('规则详情').count()) > 0, '规则详情 Drawer 打开')
      const editBtn = page.getByRole('button', { name: /编\s*辑/ })
      if ((await editBtn.count()) > 0) {
        await editBtn.first().click()
        await page.waitForTimeout(1000)
        check('edit_modal_from_detail', (await page.getByText('编辑告警规则').count()) > 0, '编辑 Modal 预填打开')
        await page.getByRole('button', { name: /取\s*消/ }).click()
        await page.waitForTimeout(600)
      } else {
        check('edit_modal_from_detail', false, '详情 Drawer 内未找到编辑按钮')
      }
      const closeBtn = page.locator('.ant-drawer-open .ant-drawer-close')
      if ((await closeBtn.count()) > 0 && (await closeBtn.first().isVisible().catch(() => false))) {
        await closeBtn.first().click()
        await page.waitForTimeout(600)
      }
    } else {
      check('detail_drawer_open', true, 'skipped: 无规则数据')
      check('edit_modal_from_detail', true, 'skipped: 无规则数据')
    }

    // 历史告警跳转入口存在
    check('history_alert_link_present', (await page.getByRole('button', { name: '历史告警' }).count()) > 0, '每行历史告警入口')
  },
}).catch((e) => { console.error(e); process.exit(1) })
