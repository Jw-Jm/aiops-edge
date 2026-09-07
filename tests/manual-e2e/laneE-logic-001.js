// PF-LOGIC-001 登录、会话与改密逻辑（非 LLM）。
// 流程：UI 登录 → 刷新会话保持（已知缺陷：内存 token 刷新即丢）→ 退出 →
//       受保护页面跳回 /login → 重新登录 → 错误当前密码改密被拒（不改 admin 密码）。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-001.js
const { ENV, uiLogin, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult } = require('./lib/laneE')

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. UI 登录成功（withSession 已完成真实 UI 登录 + scope 建立）
    check('ui_login_success', page.url().includes('/overview') || !page.url().includes('/login'), `url=${page.url()}`)
    const me1 = await (await page.request.get(`${ENV.apiBase}/me`)).json().catch(() => ({}))
    check('me_authenticated_after_login', me1?.username === ENV.username, JSON.stringify(me1).slice(0, 200))

    // 2. 刷新会话保持：已知缺陷——token 仅存内存 zustand，刷新后跳 /login
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(2500)
    const afterReload = page.url()
    const refreshKept = !/\/login/.test(afterReload)
    check('refresh_keeps_session', refreshKept,
      `刷新后 url=${afterReload}；已知缺陷：auth token 仅存内存（authStore 非持久化），刷新丢失并跳 /login`)
    notes.push('已知缺陷（如实记录）：前端会话 token 仅存内存（authStore），刷新页面后跳回 /login，需重新登录；HttpOnly cookie 会话本身仍有效。')

    // 3. 重新登录（429 退避由 uiLogin 处理）→ 受保护页面可用
    await uiLogin(page)
    const scopeResp = await page.request.post(`${ENV.apiBase}/me/scope`, {
      data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
    })
    check('relogin_scope_reestablished', scopeResp.ok(), `POST /me/scope -> ${scopeResp.status()}`)
    await spaNav(page, '/overview')
    await page.waitForTimeout(2000)
    const overviewText = (await page.locator('main').textContent().catch(() => '')) || ''
    check('protected_page_usable_after_relogin', overviewText.length > 100, `main text length=${overviewText.length}`)

    // 4. 退出登录（真实 UI：topbar .user-chip → Dropdown → 退出登录）
    await page.locator('.user-chip').first().click()
    await page.waitForTimeout(800)
    const menuItem = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
    let loggedOut = false
    if (await menuItem.isVisible().catch(() => false)) {
      await menuItem.click()
      loggedOut = true
    } else {
      // antd Dropdown 默认 hover 触发：hover 后再点
      await page.locator('.user-chip').first().hover()
      await page.waitForTimeout(800)
      const m2 = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
      if (await m2.isVisible().catch(() => false)) { await m2.click(); loggedOut = true }
    }
    await page.waitForTimeout(2500)
    check('logout_via_ui_menu', loggedOut && /\/login/.test(page.url()), `clicked=${loggedOut} url=${page.url()}`)

    // 5. 退出后受保护页面跳回 /login
    await page.goto(`${ENV.baseURL}/overview`, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(2000)
    check('protected_route_redirects_to_login_after_logout', /\/login/.test(page.url()), `url=${page.url()}`)

    // 6. 已知缺陷核查：UI 退出是否吊销服务端会话（后端无 /auth/logout 端点）
    const meAfterLogout = await page.request.get(`${ENV.apiBase}/me`)
    const meBody = await meAfterLogout.text().catch(() => '')
    check('server_session_revoked_after_ui_logout', meAfterLogout.status() !== 200,
      `UI 退出后 GET /me -> ${meAfterLogout.status()} ${meBody.slice(0, 120)}；后端无 /auth/logout 端点，退出仅清内存态，HttpOnly cookie 会话仍有效（安全/逻辑缺陷，如实记录）`)

    // 7. 改密：错误当前密码被拒（绝不修改 admin 密码）
    await uiLogin(page)
    await page.request.post(`${ENV.apiBase}/me/scope`, {
      data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
    })
    // API 层：错误当前密码
    const badPw = await page.request.post(`${ENV.apiBase}/auth/change-password`, {
      data: { current_password: 'WrongCurrent-Not-The-Password', new_password: 'Xx-Unused-1234', confirm_password: 'Xx-Unused-1234' },
    })
    const badPwBody = await badPw.text().catch(() => '')
    check('api_wrong_current_password_rejected', badPw.status() >= 400 && badPw.status() !== 500,
      `POST /auth/change-password(错误当前密码) -> ${badPw.status()} ${badPwBody.slice(0, 160)}`)
    notes.push(`已知缺陷核查：改密 401/40x 会被前端全局拦截器当作登出处理（401 → logout + 跳 /login）。`)

    // UI 层：/change-password 页面提交错误当前密码，观察真实行为
    await spaNav(page, '/change-password')
    await page.waitForTimeout(1500)
    const inputs = page.locator('input[type="password"]')
    const n = await inputs.count()
    if (n >= 3) {
      await inputs.nth(0).fill('WrongCurrent-Not-The-Password')
      await inputs.nth(1).fill('Xx-Unused-1234')
      await inputs.nth(2).fill('Xx-Unused-1234')
      await page.getByRole('button', { name: /确认|提交|保存|修改/ }).first().click()
      await page.waitForTimeout(3000)
      const urlAfter = page.url()
      const bodyText = ((await page.locator('body').textContent().catch(() => '')) || '')
      const rejected = /失败|错误|不正确|incorrect|wrong/i.test(bodyText) || /\/login/.test(urlAfter)
      check('ui_wrong_current_password_rejected', rejected,
        `提交后 url=${urlAfter}；页面提示/跳转=${(/\/login/.test(urlAfter) ? '被 401 拦截器登出并跳 /login（已知缺陷）' : bodyText.slice(0, 120))}`)
      check('ui_wrong_password_no_forced_logout', !/\/login/.test(urlAfter),
        `已知缺陷：401 被全局拦截器当登出处理 → url=${urlAfter}`)
    } else {
      check('ui_wrong_current_password_rejected', false, `/change-password 页面密码输入框数量=${n}，无法完成 UI 提交`)
    }

    // 8. admin 原密码未被改动（回归确认）
    const relogin = await page.request.post(`${ENV.apiBase}/auth/login`, {
      data: { username: ENV.username, password: ENV.password },
    })
    check('admin_password_unchanged', relogin.status() === 200, `admin/admin1234 重新登录 -> ${relogin.status()}`)

    await shot(page, 'PF-LOGIC-001-final')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass / checks.length >= 0.5 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-001', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
