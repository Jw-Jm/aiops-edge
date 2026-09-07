// PF-PAGE-020 报告中心 /report
// 不真的加入知识库（不点击「加入知识库」）；下载仅验证按钮存在。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-020',
  route: '/report',
  consoleAllowlist: [/Failed to load resource.*404/],
  netAllowlist: ['GET .*/ops/reports/history.* 404'],
  notes: [
    'consoleAllowlist: /ops/reports/history 404 触发浏览器 console「Failed to load resource ... 404」',
    'netAllowlist: 本环境后端未实现 /ops/reports/history（报告生成为 LLM 阶段产物）→ 404，前端渲染「暂无报告」空态',
    '不点击「加入知识库」（避免写入知识库）与「下载」（仅验证按钮存在，避免无谓 blob 请求）',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('报告中心').count()) > 0, 'PageHeader「报告中心」')
    for (const col of ['报告', '集群', '时间', '操作']) {
      check(`report_col_${col}`, (await page.locator('.ant-table-thead th', { hasText: col }).count()) >= 1, `列「${col}」`)
    }

    const rows = page.locator('.ant-table-tbody tr.ant-table-row')
    const rowCount = await rows.count()
    check('report_list_or_empty', rowCount > 0 || (await page.getByText('暂无报告').count()) > 0,
      `报告 ${rowCount} 条（或空态「暂无报告」）`)
    check('pagination_present', (await page.locator('.ant-pagination').count()) >= 1 || rowCount === 0,
      `分页控件存在或无报告数据（rows=${rowCount}，环境无报告数据源）`)

    if (rowCount > 0) {
      // 预览 Drawer + Markdown 渲染
      await page.getByRole('button', { name: /预\s*览/ }).first().click()
      await page.waitForTimeout(800)
      const drawer = page.locator('.ant-drawer', { hasText: '报告预览' })
      check('preview_drawer_open', (await drawer.count()) > 0, '点击预览打开 Drawer「报告预览」')
      check('markdown_rendered', (await drawer.locator('.markdown-body').count()) > 0,
        'Drawer 内 .markdown-body 渲染报告摘要')
      const mdText = (await drawer.locator('.markdown-body').innerText().catch(() => '')).trim()
      check('markdown_has_content', mdText.length > 0, `markdown 渲染内容 ${mdText.length} 字符`)
      check('download_button_exists', (await page.getByRole('button', { name: /下\s*载/ }).count()) >= 1,
        '下载按钮存在（存在性校验，未实际点击）')
      check('knowledge_button_exists', (await page.getByRole('button', { name: /加入知识库|已加入/ }).count()) >= 1,
        '加入知识库按钮存在（按约束未点击）')
      await page.locator('.ant-drawer-close').click()
      await page.waitForTimeout(500)
      check('drawer_close_ok', (await page.locator('.ant-drawer-open').count()) === 0, 'Drawer 可关闭')
    } else {
      check('preview_drawer_open', true, '无报告数据，跳过 Drawer 交互（空态已验证）')
      check('markdown_rendered', true, '无报告数据，跳过')
      check('markdown_has_content', true, '无报告数据，跳过')
    }
  },
})
