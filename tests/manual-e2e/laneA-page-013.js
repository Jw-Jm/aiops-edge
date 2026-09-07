// PF-PAGE-013 调查中心 —— Run 列表、状态显示、发起调查入口、点击进入详情。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-013',
  route: '/investigation',
  actions: async (page, check, tag) => {
    await page.getByText('调查中心', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('调查中心', { exact: true }).count()) > 0)
    await page.waitForTimeout(2500)

    // 发起调查入口
    const newBtn = page.getByRole('button', { name: '发起 AI 调查' })
    check('new_investigation_entry', (await newBtn.count()) > 0, '发起调查入口按钮')

    // Run 列表 + 状态显示
    const rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const rowCount = await rows.count()
    check('run_list_data', rowCount > 0, `Run 列表 ${rowCount} 行`)
    if (rowCount > 0) {
      const firstRowText = (await rows.first().textContent()) || ''
      check('run_id_shown', firstRowText.length > 0 && /[0-9a-f]{8}-/.test(firstRowText), 'Run ID 以 code 形式展示')
      check('run_status_badge', (await rows.first().locator('.ant-badge').count()) > 0, '状态 Badge 渲染')
      check('run_time_fields', /202[0-9]|—/.test(firstRowText), '发起时间字段展示')

      // 点击进入详情
      await page.getByRole('button', { name: '查看调查' }).first().click()
      await page.waitForTimeout(3000)
      check('run_detail_navigation', /\/investigation\/[0-9a-f-]{36}/.test(page.url()), `跳转 ${page.url()}`)
      const contentLen = (await page.content()).length
      check('run_detail_renders', contentLen > 2000, '详情页非白屏渲染')
    } else {
      check('run_id_shown', true, 'skipped: 无 Run 数据')
      check('run_status_badge', true, 'skipped: 无 Run 数据')
      check('run_time_fields', true, 'skipped: 无 Run 数据')
      check('run_detail_navigation', true, 'skipped: 无 Run 数据')
      check('run_detail_renders', true, 'skipped: 无 Run 数据')
    }
  },
}).catch((e) => { console.error(e); process.exit(1) })
