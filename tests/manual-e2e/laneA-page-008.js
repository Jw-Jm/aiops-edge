// PF-PAGE-008 虚拟机 —— 页面正常 + 无 KubeVirt 空态提示（本环境无 KubeVirt，空态即 PASS）。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-008',
  route: '/observability/vms',
  actions: async (page, check, tag) => {
    await page.getByText('虚拟机', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('虚拟机', { exact: true }).count()) > 0, 'PageHeader 渲染')
    await page.waitForTimeout(2000)

    // 空态/未安装提示（后端返回 kubevirt_not_installed=true）
    const missing = (await page.getByText('KubeVirt 未安装').count()) > 0
    const empty = (await page.getByText('暂无虚拟机').count()) > 0
    check('kubevirt_empty_state', missing || empty, missing ? '显示 KubeVirt 未安装引导（空态）' : (empty ? '显示暂无虚拟机空态' : '未出现空态提示'))
    check('no_crash_no_white_screen', (await page.locator('.ant-table, .ant-empty').count()) > 0, '表格/空态布局正常')
  },
}).catch((e) => { console.error(e); process.exit(1) })
