// PF-LOGIC-012 变更登记→调查关联逻辑。
// 已知缺陷：/ops/changes 后端 404（路由未上线）。本测试在真实 UI 登记变更
// （service=test-lanee-svc），如实记录 FAIL/BLOCKED 并附证据。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-012.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry } = require('./lib/laneE')

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. API 层证据：GET/POST /ops/changes
    const getList = await apiRetry(page, 'get', '/ops/changes?limit=10')
    check('ops_changes_get_404', getList.status === 404,
      `GET /ops/changes -> ${getList.status} ${getList.body.slice(0, 120)}（已知缺陷：后端路由未上线）`)
    const postResp = await apiRetry(page, 'post', '/ops/changes', {
      cluster_id: ENV.clusterId, service: 'test-lanee-svc', change_type: 'deployment',
      operator: 'laneE-test', content: 'PF-LOGIC-012 测试变更登记（test-laneE 标记）',
      changed_at: new Date().toISOString(),
    })
    check('ops_changes_post_404', postResp.status === 404,
      `POST /ops/changes(service=test-lanee-svc) -> ${postResp.status} ${postResp.body.slice(0, 120)}`)

    // 2. UI 层：/changes 页面登记变更 → 预期失败提示
    await spaNav(page, '/changes')
    await page.waitForTimeout(3000)
    const pageText0 = ((await page.locator('main').textContent().catch(() => '')) || '')
    check('changes_page_renders', pageText0.length > 100, `变更时间线页面文本长度=${pageText0.length}`)
    const regBtn = page.getByRole('button', { name: /登记变更/ }).first()
    if (await regBtn.isVisible().catch(() => false)) {
      await regBtn.click()
      await page.waitForTimeout(1000)
      const modal = page.locator('.ant-modal:visible')
      // 集群下拉选择
      await modal.locator('.ant-select').first().click()
      await page.waitForTimeout(600)
      await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').first().click()
      await page.waitForTimeout(400)
      await modal.locator('input').nth(1).fill('test-lanee-svc') // 服务（第 0 个 select 已处理，input 顺序按表单）
      // 变更类型下拉
      await modal.locator('.ant-select').nth(1).click()
      await page.waitForTimeout(600)
      await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').first().click()
      await page.waitForTimeout(400)
      // 操作人 / 变更内容
      const textInputs = modal.locator('input:not([type="hidden"])')
      const cnt = await textInputs.count()
      // 操作人与内容输入框定位：按 placeholder 兜底
      const opInput = modal.locator('input').filter({ hasText: '' })
      await modal.getByPlaceholder(/操作人/).fill('laneE-test').catch(() => {})
      await modal.getByPlaceholder(/内容|content|描述/).fill('PF-LOGIC-012 测试变更登记（test-laneE 标记）').catch(() => {})
      notes.push(`登记表单输入框数量=${cnt}；服务/操作人/内容已按 placeholder 尽力填充。`)
      await modal.getByRole('button', { name: /确\s*定|登\s*记|OK/ }).first().click()
      await page.waitForTimeout(3500)
      const afterText = ((await page.locator('main').textContent().catch(() => '')) || '')
      const errShown = /失败|错误|Not Found|404/i.test(afterText) || (col.badResponses.some((r) => r.url.includes('/ops/changes') && r.status === 404))
      check('ui_change_registration_fails_backend_404', errShown,
        `UI 提交登记后：页面提示含错误=${/失败|错误|Not Found|404/i.test(afterText)}；网络层 /ops/changes 404=${col.badResponses.filter((r) => r.url.includes('/ops/changes')).length} 次（已知缺陷：后端 404，登记不可用）`)
    } else {
      check('ui_change_registration_fails_backend_404', false, `变更时间线页面无"登记变更"按钮（页面文本=${pageText0.slice(0, 120)}）`)
    }

    // 3. 后端事实：MySQL change_events 表存在但 UI/API 不可达
    notes.push('后端事实：MySQL aiops.change_events 表存在（0 行）；/ops/changes 路由未在 query-api 注册（404 Not Found）→ 变更登记功能整体 BLOCKED，调查关联（change evidence）无法验证。')
    check('change_registration_end_to_end', false,
      'FAIL/BLOCKED：登记链路因 /ops/changes 后端 404 不可用（已知缺陷，如实记录）')

    await shot(page, 'PF-LOGIC-012-changes')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = 'FAIL'
  writeResult('PF-LOGIC-012', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
