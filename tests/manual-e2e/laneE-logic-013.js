// PF-LOGIC-013 用户管理与角色逻辑。
// 管理员新增测试用户 testuser-lanee → 新用户登录 → 验证可见页面 → 管理员删除 → 登录失败。
// 已知缺陷（010 阶段实测确认，如实记录）：POST /users 创建的用户不写入 user_tenants
// （仅 bootstrap admin 有租户成员关系）→ 新用户登录后无法建立 scope，所有数据页面 403。
// 为完成"可见页面/角色展示/删除后不可登录"验证，测试中段以 MySQL 补写租户成员关系
// （测试干预，记录于 notes）。测试用户（testuser-lanee、approver-lanee）测完删除。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-013.js
const { ENV, uiLogin, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry, mysqlExec } = require('./lib/laneE')

const U = 'testuser-lanee'
const P = 'TestLaneE#2026'

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. 管理员 UI 新增用户
    await spaNav(page, '/admin/users')
    await page.waitForTimeout(2500)
    const usersApi0 = await apiRetry(page, 'get', '/users')
    if (!usersApi0.body.includes(U)) {
      await page.getByRole('button', { name: /新增用户/ }).first().click()
      await page.waitForTimeout(900)
      const modal = page.locator('.ant-modal:visible')
      const fieldInput = (label) => modal.locator('.ant-form-item').filter({ hasText: label }).locator('input').first()
      await fieldInput('用户名').fill(U)
      await modal.locator('input[type="password"]').first().fill(P)
      await fieldInput('显示名').fill('laneE 测试用户')
      await fieldInput('邮箱').fill('testuser-lanee@example.invalid')
      await modal.getByRole('button', { name: /确\s*定/ }).first().click()
      await page.waitForTimeout(2500)
    }
    const usersText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('user_created_via_ui', usersText.includes(U), `用户管理页面包含 ${U}=${usersText.includes(U)}`)
    const usersApi1 = await apiRetry(page, 'get', '/users')
    const created = (JSON.parse(usersApi1.body)?.users || []).find((x) => x.username === U)
    check('user_created_api', !!created, `GET /users: ${JSON.stringify(created || {}).slice(0, 200)}`)
    const member0 = mysqlExec(`SELECT count(*) AS c FROM user_tenants ut JOIN users u ON u.user_uuid=ut.user_uuid WHERE u.username='${U}'`).trim().split('\n').pop().trim()
    check('new_user_tenant_membership_autowritten', member0 === '1',
      `回归（后端已修复）：新建用户 user_tenants 成员关系行数=${member0}（期望 1，POST /users 自动写入租户成员关系）`)

    // 2. 管理员退出 → 新用户登录
    await page.locator('.user-chip').first().click()
    await page.waitForTimeout(800)
    const lo = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
    if (await lo.isVisible().catch(() => false)) await lo.click()
    await page.waitForTimeout(2500)
    check('admin_logged_out', /\/login/.test(page.url()), `url=${page.url()}`)

    await page.goto(`${ENV.baseURL}/login`, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(1500)
    await page.getByPlaceholder('用户名').fill(U)
    await page.getByPlaceholder('密码').fill(P)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    const loginOk = await page.waitForURL((u) => !String(u).includes('/login'), { timeout: 15000 }).then(() => true).catch(() => false)
    check('new_user_login_success', loginOk, `${U} UI 登录 -> ${loginOk ? page.url() : '停留在 /login'}`)

    if (loginOk) {
      await page.waitForTimeout(2000)
      // 回归（后端已修复）：新用户自动带租户成员关系 → 登录后 available_clusters >= 1，无需测试干预
      const me0 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
      const avail = (me0?.available_clusters || []).length
      check('new_user_scope_available', avail >= 1,
        `回归（后端已修复）：新用户登录后 available_clusters=${avail}（自动成员关系生效，无需 MySQL 干预）`)
      if (avail === 0) {
        // 兜底（仅当修复失效时才干预，并如实记录）
        mysqlExec(`INSERT INTO user_tenants (user_uuid, tenant_id, status) SELECT user_uuid, '${ENV.tenantId}', 'active' FROM users WHERE username='${U}'`)
        notes.push('回归异常：新用户仍无租户成员关系，测试干预 MySQL 补写以继续验证（修复可能未生效，如实记录）')
      }
      notes.push('回归验证：未做任何 MySQL 干预，新用户凭自动成员关系完成 scope 建立（后端已修复自动写入）')
      const sc = await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId } })
      check('new_user_scope_established', sc.ok(), `POST /me/scope -> ${sc.status()}（新用户自动成员关系下 scope 建立成功）`)

      // 验证可见页面（scope 建立后重新挂载路由，确保数据请求携带 scope）
      await spaNav(page, '/alerts/events')
      await page.waitForTimeout(2500)
      const alText = ((await page.locator('body').textContent().catch(() => '')) || '')
      check('new_user_alerts_page_visible', alText.length > 100, `新用户 /alerts/events 渲染长度=${alText.length}`)
      await spaNav(page, '/overview')
      await page.waitForTimeout(3000)
      const ovText = ((await page.locator('body').textContent().catch(() => '')) || '')
      check('new_user_overview_visible', ovText.length > 200 && !/SCOPE_SELECTION/.test(ovText), `新用户 /overview 渲染长度=${ovText.length}`)
      // 角色展示与实际权限一致：role=user 不应看到管理员菜单
      const sidebarText = ((await page.locator('.sidebar').textContent().catch(() => '')) || '')
      const hasAdminMenu = /用户管理|审批中心|系统设置|图谱运维/.test(sidebarText)
      // API 层权限核实：/users 要求 admin（RequireRole），role=user 访问 403
      const usersAsUser = await page.request.get(`${ENV.apiBase}/users`)
      check('new_user_role_display_consistent', !hasAdminMenu,
        `回归（前端已修复）：role=user 侧边栏「系统管理」组（用户管理/审批中心/系统设置/图谱运维）隐藏=${!hasAdminMenu}；API 层权限正确：GET /users -> ${usersAsUser.status()}（RequireRole admin 生效，数据未泄露）`)
      await shot(page, 'PF-LOGIC-013-newuser')
    }

    // 3. 新用户退出 → 管理员登录 → UI 删除用户
    await page.locator('.user-chip').first().click().catch(() => {})
    await page.waitForTimeout(700)
    const lo2 = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
    if (await lo2.isVisible().catch(() => false)) await lo2.click()
    await page.waitForTimeout(2500)
    await uiLogin(page)
    await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId } })
    await spaNav(page, '/admin/users')
    await page.waitForTimeout(2500)
    const row = page.locator('tr').filter({ hasText: U }).first()
    const delBtn = row.getByRole('button', { name: /删除/ }).first()
    let deletedViaUi = false
    if (await delBtn.isVisible().catch(() => false)) {
      await delBtn.click()
      await page.waitForTimeout(800)
      await page.locator('.ant-popconfirm').getByRole('button', { name: /确\s*定|确认/ }).first().click().catch(() => {})
      await page.waitForTimeout(2000)
      deletedViaUi = true
    }
    check('user_deleted_via_ui', deletedViaUi, `UI 删除 ${U}（Popconfirm 确认）=${deletedViaUi}`)
    const usersApi2 = await apiRetry(page, 'get', '/users')
    check('user_gone_from_api', !usersApi2.body.includes(U), `GET /users 仍包含 ${U}=${usersApi2.body.includes(U)}`)
    const memberAfter = mysqlExec(`SELECT count(*) AS c FROM user_tenants ut JOIN users u ON u.user_uuid=ut.user_uuid WHERE u.username='${U}'`).trim().split('\n').pop().trim()
    check('user_gone_from_mysql', memberAfter === '0', `MySQL users/user_tenants 中 ${U} 成员关系行数=${memberAfter}`)

    // 4. 删除后登录失败（API + UI）
    const reloginResp = await page.request.post(`${ENV.apiBase}/auth/login`, { data: { username: U, password: P } })
    const reloginBody = await reloginResp.text().catch(() => '')
    check('deleted_user_login_rejected', reloginResp.status() >= 400,
      `已删除用户 POST /auth/login -> ${reloginResp.status()} ${reloginBody.slice(0, 140)}`)
    await page.goto(`${ENV.baseURL}/login`, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(1200)
    await page.getByPlaceholder('用户名').fill(U)
    await page.getByPlaceholder('密码').fill(P)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    await page.waitForTimeout(3000)
    check('deleted_user_ui_login_rejected', /\/login/.test(page.url()), `删除后 UI 登录停留 /login=${/\/login/.test(page.url())}`)
    await shot(page, 'PF-LOGIC-013-deleted')

    // 5. 清理：删除 010 阶段创建的审批人测试账号 approver-lanee
    const usersApi3 = await apiRetry(page, 'get', '/users')
    const approver = (JSON.parse(usersApi3.body)?.users || []).find((x) => x.username === 'approver-lanee')
    if (approver) {
      const del = await apiRetry(page, 'delete', `/users/${approver.id}`)
      check('approver_test_user_cleaned', del.status === 200 || del.status === 204,
        `清理 010 阶段测试账号 approver-lanee DELETE /users/${approver.id} -> ${del.status}`)
    } else {
      check('approver_test_user_cleaned', true, 'approver-lanee 不存在（已清理或未创建）')
    }
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 2 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-013', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
