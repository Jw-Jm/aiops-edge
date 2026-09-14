// PF-PAGE-015 调查详情 /investigation/:runId
// 优先用真实历史 Run 测详情页状态/计划/Tool/Evidence 展示与刷新恢复；无 Run 则假 ID 错误态并记 BLOCKED。
const { runLaneB, findLatestRunWithEvidence } = require('./lib/laneB')

;(async () => {
  const found = await findLatestRunWithEvidence()

  if (!found) {
    await runLaneB({
      id: 'PF-PAGE-015',
      route: '/investigation/00000000-0000-0000-0000-000000000000',
      netAllowlist: ['GET .*ai/runs/00000000.* 404'],
      notes: ['netAllowlist: 假 Run ID 详情接口 404 属预期（无历史 Run 的错误态路径）'],
      mutate: (s) => { s.status = 'BLOCKED'; s.blockedReason = '环境中无任何历史 AI Run，无法测试详情页真实数据展示' },
      setup: async (page, check) => {
        check('detail_page_renders_error_state', (await page.getByText('智能调查').count()) > 0, '假 ID 下页面不白屏')
      },
    })
    return
  }

  const runId = found.run.run_id
  await runLaneB({
    id: 'PF-PAGE-015',
    route: `/investigation/${runId}`,
    consoleAllowlist: [],
    netAllowlist: [],
    notes: [
      `使用真实历史 Run ${runId}（intent=${found.run.intent}, status=${found.run.status}，evidence ${found.count} 条）`,
      'consoleAllowlist 空：详情页为只读展示页，不应产生 console error',
      'netAllowlist 空：runs/evidences/tools/graph-context 均为正常授权 2xx 请求',
      '取消操作未测：仅当页面提供且会中断 Run（Run 为历史数据，不执行变更类操作）',
      '回归 20260906：auth token 已持久化 sessionStorage，整页 reload 后应停留在详情页（修复前回 /login）',
    ],
    setup: async (page, check) => {
      check('page_title_visible', (await page.getByText('智能调查').count()) > 0, 'PageHeader「智能调查」')
      check('run_id_in_header', (await page.getByText(`Run ${runId}`).count()) >= 1, 'header desc 含 Run ID')

      // Scope 与 Intent 卡
      check('scope_intent_card', (await page.getByText('Scope 与 Intent').count()) > 0)
      for (const label of ['Tenant', 'Cluster', 'Resource']) {
        check(`scope_field_${label}`, (await page.getByText(label, { exact: true }).count()) >= 1)
      }
      check('scope_field_Intent', (await page.getByText('Intent：').count()) >= 1, 'Intent 字段（antd 文本含全角冒号）')
      const statusTag = await page.locator('.ant-tag').allInnerTexts()
      check('status_tag_present', statusTag.some((t) => /created|running|partial|success|failed|pending/i.test(t)),
        `状态 Tag: ${statusTag.join(',')}`)

      // Graph Context / Tool / Evidence / Plan 卡
      check('tool_card_visible', (await page.getByText('工具活动 (真实 ToolRun)').count()) > 0,
        'ToolRun 卡有数据或显示「暂无真实 ToolRun（数据源为空，不伪造）」空态')
      check('evidence_card_visible', (await page.getByText('Evidence', { exact: true }).count()) >= 1)
      check('plan_card_visible', (await page.getByText('Plan / Hypothesis / Root Cause / Action').count()) > 0)
      check('sse_event_card_visible', (await page.getByText('执行事件（持久化 SSE）').count()) > 0)

      // 数据反查：页面 Resource 应含 API 返回的 target_resource_id
      const detail = await page.request.get(`http://localhost:30253/api/v1/ai/runs/${runId}`)
      const runBody = await detail.json().catch(() => null)
      if (runBody?.run?.target_resource_id) {
        check('resource_matches_api', (await page.getByText(runBody.run.target_resource_id).count()) >= 1,
          `页面 Resource 显示 API target_resource_id=${runBody.run.target_resource_id}`)
      } else {
        check('resource_matches_api', true, 'run detail 无 target_resource_id，跳过反查')
      }

      // 刷新恢复（真实整页刷新：authStore 为内存态，观察是否回登录页）
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(2500)
      const backOnLogin = /\/login/.test(page.url())
      check('refresh_recovery', !backOnLogin && (await page.getByText('Scope 与 Intent').count()) > 0,
        `整页刷新后 url=${page.url()}${backOnLogin ? ' —— 回退到 /login（内存态 authStore 无会话恢复，cookie 会话未用于恢复路由态）' : '，详情页恢复'}`)
    },
  })
})().catch((e) => { console.error(e); process.exit(1) })
