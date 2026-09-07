// PF-PAGE-014 发起调查 /investigation/new
// 不提交创建 Run（Run 执行依赖 LLM，属后续阶段）；仅测表单显示、必填校验、只读语义提示。
const { runLaneB } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-014',
  route: '/investigation/new',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：普通表单页不应产生任何 console error',
    'netAllowlist 空：页面仅读取 clusters/me 等正常 2xx 接口',
    '按测试约束不点击有效提交：Run 创建依赖 LLM，属后续阶段；仅空表单校验提交',
  ],
  setup: async (page, check) => {
    check('page_title_visible', await page.getByText('发起 AI 调查').count() > 0, 'PageHeader 标题「发起 AI 调查」')
    check('readonly_semantics_hint', await page.getByText('显式人工触发').count() > 0,
      '描述含「显式人工触发；不随页面加载/告警到达自动创建」→ 默认只读语义成立')
    check('cluster_select_present', await page.locator('.ant-select').count() >= 2, 'Canonical Cluster + 目标类型两个 Select')
    check('resource_input_present', (await page.locator('input[placeholder="svc/checkout"]').count()) === 1)
    check('symptom_textarea_present', (await page.locator('textarea[placeholder="service error rate spike"]').count()) === 1)
    check('submit_button_present', (await page.getByRole('button', { name: '发起调查' }).count()) === 1)
    check('cancel_button_present', (await page.getByRole('button', { name: /取\s*消/ }).count()) >= 1)

    // 必填校验：resourceId / symptom 为空时提交 → 出现校验错误且不跳转（cluster/targetType 有默认值）
    await page.getByRole('button', { name: '发起调查' }).click()
    await page.waitForTimeout(1000)
    const errCount = await page.locator('.ant-form-item-explain-error').count()
    const errTexts = await page.locator('.ant-form-item-explain-error').allInnerTexts()
    check('required_validation_shown', errCount >= 2, `空提交显示 ${errCount} 条必填错误: ${errTexts.join(' / ')}`)
    check('no_submit_navigation', page.url().endsWith('/investigation/new'), `url=${page.url()}，未创建 Run 未跳转`)
  },
})
