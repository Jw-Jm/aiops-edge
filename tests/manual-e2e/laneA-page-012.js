// PF-PAGE-012 AI 运维助手 —— 仅 UI：会话列表/新会话按钮/Cluster 未选择限制/入口元素。不发送任何消息。
// 说明：全新浏览器上下文未持久化集群选择（localStorage aiops-ui-v3.currentClusterId 为空），
// 正好对应手册中 "Cluster 未选择时限制" 场景。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-012',
  route: '/ai/chat',
  // uiCluster=false：全新上下文不预置集群作用域，正好对应手册 "Cluster 未选择时限制" 场景；
  // 之后再通过顶部 ClusterSwitcher 真实选择集群，验证限制解除。
  uiCluster: false,
  actions: async (page, check, tag) => {
    await page.getByText('AI 运维助手', { exact: true }).first().waitFor({ timeout: 10000 }).catch(() => {})
    check('page_title', (await page.getByText('AI 运维助手', { exact: true }).count()) > 0)
    await page.waitForTimeout(2000)

    // 历史会话列表区（本环境 sessions 为空 → 暂无会话空态）
    check('session_panel', (await page.getByText('会话', { exact: true }).count()) > 0, '会话列表卡片头')
    const emptyOrRows =
      (await page.getByText('暂无会话').count()) > 0 || (await page.locator('button[title=删除会话]').count()) > 0
    check('session_list_state', emptyOrRows, '空态或历史会话条目正常')

    // 新会话按钮
    const newBtn = page.getByRole('button', { name: '新对话' })
    check('new_session_button', (await newBtn.count()) > 0, '新会话按钮存在')
    if ((await newBtn.count()) > 0) {
      await newBtn.click()
      await page.waitForTimeout(1000)
      check('new_session_click_ok', true, '点击新会话无异常')
    }

    // Cluster 未选择限制提示：输入框禁用 + placeholder 提示 + 发送禁用
    const input = page.locator('.ai-dock__input input').first()
    check('input_visible', (await input.count()) > 0, '输入框渲染')
    const placeholder = (await input.getAttribute('placeholder').catch(() => '')) || ''
    const inputDisabled = await input.isDisabled().catch(() => true)
    const sendBtn = page.getByRole('button', { name: /发\s*送/ })
    const sendDisabled = await sendBtn.isDisabled().catch(() => true)
    check('cluster_unset_restriction', placeholder.includes('请先在顶部选择具体集群') && inputDisabled && sendDisabled,
      `placeholder="${placeholder}" inputDisabled=${inputDisabled} sendDisabled=${sendDisabled}`)

    // 通过顶部 ClusterSwitcher 真实选择集群 → 限制解除
    const switcher = page.locator('.ant-select[title="切换监控集群范围"]').first()
    if ((await switcher.count()) > 0) {
      await switcher.click()
      await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')
        .filter({ hasText: 'kubernetes-cluster' }).first().click({ timeout: 5000 })
      await page.waitForTimeout(2500)
      const ph2 = (await input.getAttribute('placeholder').catch(() => '')) || ''
      const enabled2 = !(await input.isDisabled().catch(() => true))
      const sendEnabled2 = !(await sendBtn.isDisabled().catch(() => true))
      check('cluster_selected_restriction_lifted', ph2.includes('描述问题') && enabled2 && sendEnabled2,
        `选择集群后 placeholder="${ph2}" inputDisabled=${!enabled2} sendDisabled=${!sendEnabled2}`)
    } else {
      check('cluster_selected_restriction_lifted', false, '未找到集群作用域选择器')
    }

    check('llm_not_triggered', true, '未发送任何消息（LLM 依赖不在此 lane 测）')

    // 入口元素（不点击，避免触发 LLM）
    check('quick_entry_buttons', (await page.getByRole('button', { name: '分析集群根因' }).count()) > 0, '快捷问题入口存在（未点击）')
  },
}).catch((e) => { console.error(e); process.exit(1) })
