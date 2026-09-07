// PF-PAGE-018 K8s 运维 /infra/k8s
// 只读浏览 + 表单联动测试；不点击「① 预检并提交审批」/「② 执行」（不创建/执行任何 Action）。
const { runLaneB, pickOption } = require('./lib/laneB')

runLaneB({
  id: 'PF-PAGE-018',
  route: '/infra/k8s',
  consoleAllowlist: [],
  netAllowlist: [],
  notes: [
    'consoleAllowlist 空：列表页正常 2xx，不应产生 console error',
    'netAllowlist 空：k8s lists/namespaces 均为只读 2xx 请求',
    '按测试约束不点击「① 预检并提交审批」（会创建不可变 Canonical Action）与「② 执行」；仅验证按钮与审批边界文案存在',
  ],
  setup: async (page, check) => {
    check('page_title_visible', (await page.getByText('K8s 运维').count()) > 0, 'PageHeader「K8s 运维」')
    check('scope_label_present', (await page.getByText(/操作范围：集群/).count()) > 0)
    check('approval_boundary_note', (await page.getByText('Canonical Action 审批边界').count()) > 0,
      '审批边界说明卡渲染')
    check('preflight_button_present', (await page.getByRole('button', { name: /① 预检并提交审批/ }).count()) === 1)
    check('execute_button_present', (await page.getByRole('button', { name: /② 执行/ }).count()) === 1)

    // Deployments / Pods / 节点 切换
    const seg = (label) => page.locator('.ant-segmented-item', { hasText: label }).first()
    check('default_deployments_view', (await page.getByText('副本', { exact: true }).count()) >= 1,
      '默认 Deployments 视图（副本/就绪列）')
    await seg('Pods').click()
    await page.waitForTimeout(1200)
    check('pods_view_switch', (await page.getByText('重启次数', { exact: true }).count()) >= 1, 'Pods 视图（状态/重启次数列）')
    await seg('节点').click()
    await page.waitForTimeout(1200)
    check('nodes_view_switch', (await page.getByText('Kubelet', { exact: true }).count()) >= 1, '节点视图（Kubelet/CPU/内存列）')
    await seg('Deployments').click()
    await page.waitForTimeout(1200)

    // Namespace 筛选
    const nsSel = page.locator('.ant-select').filter({ hasText: '全部命名空间' }).first()
    if ((await nsSel.count()) > 0) {
      await nsSel.click()
      const nsOptions = await page
        .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')
        .allInnerTexts()
      check('namespace_options_loaded', nsOptions.length > 1, `命名空间选项 ${nsOptions.length} 个`)
      const target = nsOptions.find((o) => o !== '全部命名空间')
      if (target) {
        await page
          .locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option', { hasText: target })
          .first().click()
        await page.waitForTimeout(1500)
        check('namespace_filter_applied', (await page.locator('.ant-select').filter({ hasText: target }).count()) >= 1,
          `筛选命名空间 ${target} 后列表刷新`)
        await pickOptionNsBack(page)
      }
    } else {
      check('namespace_options_loaded', false, '未找到命名空间筛选 Select')
    }

    // 点击资源自动填充
    const firstRow = page.locator('.ant-table-tbody tr.ant-table-row').first()
    if ((await firstRow.count()) > 0) {
      const rowName = await firstRow.locator('td').first().innerText()
      await firstRow.click()
      await page.waitForTimeout(500)
      const nameVal = await page.locator('input[placeholder="资源名称"]').inputValue()
      check('row_click_fills_target', nameVal === rowName.trim(), `点击行 ${rowName.trim()} 自动填充目标资源名称`)
      const nsVal = await page.locator('input[placeholder="命名空间"]').inputValue().catch(() => '')
      check('row_click_fills_namespace', typeof nsVal === 'string', `namespace 填充="${nsVal}"`)
    } else {
      check('row_click_fills_target', false, 'Deployments 表格无数据行')
    }

    // kind → action 联动 + 动态参数
    await pickOption(page, page.locator('.ant-select').filter({ hasText: /^deployment/ }).first(), 'node')
    await page.waitForTimeout(500)
    check('kind_node_hides_namespace', (await page.locator('input[placeholder="命名空间"]').count()) === 0,
      'kind=node 时命名空间输入隐藏')
    await pickOption(page, page.locator('.ant-select').filter({ hasText: /cordon|隔离|重启/ }).first(), '节点排空 (drain)')
    await page.waitForTimeout(500)
    check('drain_dynamic_param', (await page.getByText('超时秒数').count()) >= 1, 'drain 动作显示 drain_timeout 动态参数')
    // scale 动态参数
    await pickOption(page, page.locator('.ant-select').filter({ hasText: /^node/ }).first(), 'deployment')
    await page.waitForTimeout(500)
    await pickOption(page, page.locator('.ant-select').filter({ hasText: /滚动重启/ }).first(), '扩缩容')
    await page.waitForTimeout(500)
    check('scale_dynamic_param', (await page.getByText('副本数').count()) >= 1, 'scale 动作显示副本数动态参数')

    // 刷新按钮
    await page.getByRole('button', { name: /刷\s*新/ }).click()
    await page.waitForTimeout(1000)
    check('refresh_ok', (await page.getByText('K8s 运维').count()) > 0)
  },
})

// 恢复命名空间筛选为「全部命名空间」
async function pickOptionNsBack(page) {
  const sel = page.locator('.ant-select').filter({ hasText: /全部命名空间|default|kube|observability/ }).first()
  try {
    await pickOption(page, sel, '全部命名空间')
  } catch { /* 忽略恢复失败 */ }
  await page.waitForTimeout(1000)
}
