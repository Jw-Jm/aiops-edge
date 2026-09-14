// PF-PAGE-016 Evidence 详情 /investigation/:runId/evidence/:evidenceId
// 有 Run 则取一个 evidence 深链测试；并测不存在的 evidence 提示正常。
// 回归 20260906：auth token 已持久化 sessionStorage，新标签/刷新直达深链应不再回 /login。
const { runLaneB, findLatestRunWithEvidence, uiLogin, gotoApp } = require('./lib/laneB')

;(async () => {
  const found = await findLatestRunWithEvidence()

  if (!found) {
    await runLaneB({
      id: 'PF-PAGE-016',
      route: '/investigation/00000000-0000-0000-0000-000000000000/evidence/11111111-1111-1111-1111-111111111111',
      netAllowlist: ['GET .*ai/runs/00000000.* 404'],
      notes: ['netAllowlist: 假 Run ID 404 属预期'],
      mutate: (s) => { s.status = 'BLOCKED'; s.blockedReason = '环境中无任何历史 AI Run/Evidence，无法测试真实 Evidence 深链' },
      setup: async (page, check) => {
        check('evidence_page_renders', (await page.getByText('证据详情').count()) > 0, '假 ID 下不白屏')
      },
    })
    return
  }

  const { run, evidence } = found
  const runId = run.run_id
  const evId = evidence.evidence_id
  const goodPath = `/investigation/${runId}/evidence/${evId}`
  const badPath = `/investigation/${runId}/evidence/00000000-0000-0000-0000-000000000000`

  await runLaneB({
    id: 'PF-PAGE-016',
    route: goodPath,
    consoleAllowlist: [/Failed to load resource.*404/],
    // 不存在的 evidence → GET .../evidences/00000000... → 404，属预期错误态路径
    netAllowlist: [`GET .*evidences/00000000-0000-0000-0000-000000000000.* 404`],
    notes: [
      `真实深链：Run ${runId} Evidence ${evId}（type=${evidence.evidence_type}）`,
      'netAllowlist: 故意访问不存在的 evidence → 后端 404，验证「证据不存在」错误提示，属预期',
      'consoleAllowlist: 预期 404 探测会触发浏览器 console「Failed to load resource ... 404」，与错误态路径对应',
    ],
    setup: async (page, check) => {
      // 1) 不存在的 evidence → 明确错误提示（SPA 导航，验证 404 错误态 UI）
      await gotoApp(page, badPath)
      await page.waitForTimeout(800)
      check('missing_evidence_alert', (await page.getByText('证据不存在').count()) > 0,
        '不存在 evidence 显示「证据不存在」Alert，不白屏不崩溃')

      // 2) 真实 evidence 深链（SPA 导航后页面内容）
      await gotoApp(page, goodPath)
      await page.waitForTimeout(800)
      check('deep_link_title', (await page.getByText('证据详情').count()) > 0, 'Evidence 详情页标题')
      check('meta_card_visible', (await page.getByText('Evidence 元数据').count()) > 0)
      check('meta_tenant_cluster', (await page.getByText('91771a6e-9c2d-11f1-8271-bea176fe9f9f').count()) >= 1,
        'Cluster 元数据与 Run 归属一致')
      check('evidence_type_tag', (await page.getByText(evidence.evidence_type).count()) >= 1,
        `来源/类型 Tag 显示 evidence_type=${evidence.evidence_type}`)
      check('fact_card_visible', (await page.getByText('Fact（事实内容）').count()) > 0, 'Fact 内容卡渲染')
      check('back_to_run_button', (await page.getByRole('button', { name: '返回调查' }).count()) === 1)

      // 3) 返回 Run 正常
      await page.getByRole('button', { name: '返回调查' }).click()
      await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {})
      await page.waitForTimeout(500)
      check('back_to_run_navigation', page.url().endsWith(`/investigation/${runId}`), `url=${page.url()}`)

      // 4) fresh-load 直达（新标签/刷新场景）：如实记录是否被重定向回 /login
      await page.goto(`http://localhost:30253${goodPath}`, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(2000)
      const redirected = /\/login/.test(page.url())
      check('deep_link_fresh_load_direct', !redirected,
        `整页加载深链 ${goodPath} → ${page.url()}${redirected ? ' —— 被重定向回 /login（内存态 authStore，无会话恢复），Deep Link 直达不成立' : ' 直达成功'}`)

      // 5) 恢复登录并回到有效 evidence 页（保证最终截图为有效状态）
      if (redirected) {
        await uiLogin(page)
        await gotoApp(page, goodPath)
      }
      await page.waitForTimeout(600)
      check('final_state_valid', (await page.getByText('证据详情').count()) > 0, '恢复后 Evidence 详情页可见')
    },
  })
})().catch((e) => { console.error(e); process.exit(1) })
