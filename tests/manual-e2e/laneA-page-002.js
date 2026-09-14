// PF-PAGE-002 首次修改密码 —— 仅测校验分支，绝不成功修改 admin 密码。
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'PF-PAGE-002',
  route: '/change-password',
  netAllowlist: [
    // 故意用错误当前密码提交 → 后端返回 4xx 属预期（校验提示用例）
    'POST .*auth/change-password 4',
  ],
  consoleAllowlist: [
    // 同上：400 响应触发 Chromium 资源级 console error
    'Failed to load resource.*4[0-9][0-9]',
  ],
  actions: async (page, check, tag) => {
    await page.getByText('首次登录需要修改密码').waitFor({ timeout: 8000 }).catch(() => {})
    check('page_heading_visible', await page.getByText('首次登录需要修改密码').count() > 0, '标题与布局')
    check('three_password_inputs', (await page.locator('input[type=password]').count()) === 3, '当前/新/确认三个输入框')

    const inputs = page.locator('input[type=password]')
    const submit = page.getByRole('button', { name: '修改密码并继续' })

    // 1) 空值提交 → 必填校验
    await submit.click()
    await page.waitForTimeout(700)
    check('empty_submit_validation', (await page.getByText('请输入当前密码').count()) > 0, '空提交出现必填提示')

    // 2) 新密码过短 → 最少 8 位校验
    await inputs.nth(0).fill('SomeCurrent1!')
    await inputs.nth(1).fill('short')
    await inputs.nth(2).fill('short')
    await submit.click()
    await page.waitForTimeout(700)
    check('min_length_validation', (await page.getByText('新密码至少 8 位').count()) > 0, '短密码被拦截')

    // 3) 两次不一致 → 一致性校验
    await inputs.nth(1).fill('ValidNewPass123')
    await inputs.nth(2).fill('OtherPass999')
    await submit.click()
    await page.waitForTimeout(700)
    check('mismatch_validation', (await page.getByText('两次输入的新密码不一致').count()) > 0, '不一致提示出现')

    // 4) 错误当前密码 → 期望页面出现错误提示，且密码绝不被修改、不被登出
    await inputs.nth(0).fill('definitely-wrong-current-password')
    await inputs.nth(1).fill('TmpProbe#2026a')
    await inputs.nth(2).fill('TmpProbe#2026a')
    await submit.click()
    let toasts = ''
    try {
      await page.locator('.ant-message-notice').first().waitFor({ timeout: 4000 })
      await page.waitForTimeout(1000) // 等第二条 toast（错误提示）出现
      toasts = (await page.locator('.ant-message-notice').allTextContents()).join(' | ')
    } catch { /* no toast */ }
    await page.waitForTimeout(1200)
    // 回归 20260906：后端改 400 + 前端拦截器豁免 /auth/change-password 的 401/400 →
    // 页面应显示错误提示（invalid_current_password / 密码修改失败），且不被登出跳 /login
    check('wrong_current_password_hint',
      /invalid_current_password|密码修改失败|密码|错误|失败/.test(toasts.replace('登录成功', '')) && page.url().includes('/change-password'),
      `toasts="${toasts}" url=${page.url()}（回归：后端 400 + 拦截器豁免，错误提示在页面展示且未登出）`)
    // 密码未被修改的权威反查：原密码仍可登录
    const relogin = await page.request.post(`${process.env.API_BASE || 'http://localhost:30253/api/v1'}/auth/login`, {
      data: { username: 'admin', password: 'admin1234' },
    })
    check('admin_password_unchanged', relogin.ok(), `原密码 admin1234 仍可登录（HTTP ${relogin.status()}），密码未被修改`)
  },
}).catch((e) => { console.error(e); process.exit(1) })
