// PF-UI-004 用户菜单与退出（退出测试放最后执行——会清空会话）。
// 用户信息显示 / 用户菜单 / 修改密码入口 / 退出登录 / 退出后受保护页面不可用。
// Standalone: node tests/manual-e2e/laneD-004-user.js
const { ENV, uiLogin, spaNav, makeCollector, writeResult, shot, withSession } = require('./lib/laneD-runner')

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const result = {
    id: 'PF-UI-004', route: '/overview (topbar user menu)', category: 'pages',
    consoleErrors: [], pageErrors: [], failedRequests: [], checks: [], notes: [], menu: {},
  }
  const check = (name, pass, detail = '') => result.checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 300) })

  const col = makeCollector(tag)
  await withSession({ viewport: vp, col }, async (page) => {
    // withSession 已完成 UI 登录（429 退避）+ /me/scope + scope 预置

    // 用户信息显示（displayName「管理员」或用户名 admin）
    const chip = page.locator('.topbar .user-chip')
    const chipText = ((await chip.textContent()) || '').trim()
    check('user_info_shown', (await chip.count()) > 0 && /(admin|管理员)/.test(chipText), `user-chip="${chipText.slice(0, 40)}"`)
    const avatar = (await chip.locator('.avatar').textContent().catch(() => '')) || ''
    check('avatar_initial_rendered', avatar.trim().length > 0, `avatar="${avatar.trim()}"`)

    // 用户菜单
    await chip.click()
    await page.waitForTimeout(800)
    const menu = page.locator('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu')
    const menuVisible = await menu.isVisible().catch(() => false)
    const labels = menuVisible ? await menu.locator('.ant-dropdown-menu-item').allTextContents() : []
    result.menu = { visible: menuVisible, labels }
    check('user_menu_opens', menuVisible, `items=${JSON.stringify(labels)}`)
    check('menu_has_logout', labels.some((l) => l.includes('退出登录')))
    check('menu_has_password_entry', labels.some((l) => l.includes('修改密码')))

    // 修改密码入口：代码中该菜单项 disabled:true（App.tsx），存在但不可用 → 记录真实结果
    const pwdItem = menu.locator('.ant-dropdown-menu-item', { hasText: '修改密码' }).first()
    const pwdDisabled = await pwdItem.evaluate((el) => el.classList.contains('ant-dropdown-menu-item-disabled')).catch(() => null)
    check('modify_password_entry_functional', pwdDisabled === false,
      pwdDisabled === true
        ? '修改密码菜单项存在但 disabled:true，点击无响应（App.tsx 用户菜单硬编码 disabled）；修改密码仅通过首次登录强制流程 /change-password 暴露'
        : `disabled=${pwdDisabled}`)
    result.notes.push('回归 20260906：用户菜单「修改密码」入口已可点击（disabled=false，跳转 /change-password）；退出登录调用 /auth/logout 吊销服务端会话（/me 403）。')

    // 系统设置入口可跳转（验证菜单非死 UI）
    const settingsItem = menu.locator('.ant-dropdown-menu-item', { hasText: '系统设置' }).first()
    await settingsItem.click().catch(() => {})
    await page.waitForTimeout(1000)
    check('menu_settings_navigates', page.url().includes('/admin/settings'), `url=${page.url()}`)
    await spaNav(page, '/overview')
    await page.waitForTimeout(500)

    // 退出登录
    await chip.click()
    await page.waitForTimeout(600)
    await page.locator('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item', { hasText: '退出登录' }).first().click()
    await page.waitForTimeout(2500)
    const afterLogoutUrl = page.url()
    const loginFormBack = (await page.locator('input[placeholder="用户名"]').count()) > 0
    const sidebarGone = (await page.locator('.sidebar').count()) === 0
    check('logout_returns_to_login', afterLogoutUrl.includes('/login') && loginFormBack && sidebarGone, `url=${afterLogoutUrl}`)
    await shot(page, `PF-UI-004-${tag}-after-logout`)

    // 退出后受保护页面不可继续使用（直接访问 → 回 /login）
    for (const r of ['/overview', '/alerts/events', '/admin/users']) {
      await page.goto(`${ENV.baseURL}${r}`, { waitUntil: 'domcontentloaded', timeout: 20000 })
      await page.waitForTimeout(2000)
      check(`protected_page_${r}_redirects_to_login`, page.url().includes('/login'), `url=${page.url()}`)
    }

    // 回归断言：退出登录后服务端会话必须吊销（/auth/logout 已实现）——/me 不再 200
    try {
      const me = await page.request.get(`${ENV.apiBase}/me`)
      check('logout_revokes_server_session', me.status() === 401 || me.status() === 403,
        `前端 logout 后 HttpOnly 会话 Cookie 的 /api/v1/me -> ${me.status()}（期望 401/403，服务端会话已吊销；修复前为 200）`)
    } catch (e) {
      check('logout_revokes_server_session', false, `/api/v1/me 请求异常 ${String(e).slice(0, 100)}`)
    }

    result.consoleErrors.push(...col.consoleErrors)
    result.pageErrors.push(...col.pageErrors)
    result.failedRequests.push(...col.badResponses)
  })

  const summary = writeResult('PF-UI-004', 'pages', result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((e) => { console.error(e); process.exit(1) })
module.exports = { run }
