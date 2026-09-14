// PF-PAGE-026 404/非法路由 /definitely-not-a-page
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-026',
  route: '/definitely-not-a-page',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：404 页为纯前端 Result 组件，不应产生 console error',
    'netAllowlist 空：非法路由为前端路由拦截，无 API 请求（AppLayout 的告警轮询为正常 2xx）',
  ],
  setup: async (page, check) => {
    check('notfound_displayed', (await page.getByText('页面不存在').count()) > 0, '非法 URL 显示 NotFound「页面不存在」')
    check('notfound_404_status', (await page.locator('.ant-result-404, .ant-result-image').count()) >= 1,
      'antd Result status=404（404 以 .ant-result-image 图片呈现，无文本「404」）')
    const bodyLen = (await page.content()).length
    check('no_white_screen', bodyLen > 500, `页面内容长度 ${bodyLen}，非白屏`)

    // 返回合法页面
    await page.getByRole('button', { name: '返回工作台' }).click()
    await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
    await page.waitForTimeout(800)
    check('back_to_valid_page', page.url().endsWith('/overview') && (await page.getByText('工作台').count()) >= 1,
      `点击「返回工作台」→ ${page.url()}`)

    // 浏览器后退 → 回到 404；前进 → 回到合法页
    await page.goBack()
    await page.waitForTimeout(800)
    check('history_back_404', page.url().includes('/definitely-not-a-page') &&
      (await page.getByText('页面不存在').count()) > 0, '后退恢复 404 页')
    await page.goForward()
    await page.waitForTimeout(800)
    check('history_forward_overview', page.url().endsWith('/overview'), `前进恢复 ${page.url()}`)
  },
})
