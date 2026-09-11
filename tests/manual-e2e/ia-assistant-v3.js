// IA-ASSISTANT-V3-001: the cluster assistant must answer inside a frozen scope
// with the six contractual sections and must never offer a direct execution path.
//
// A real browser run is the only acceptable evidence: an empty conversation, a
// silent spinner or a swallowed error are all FAIL, never PASS.
const { ENV, makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 1024, height: 900 },
]

const SECTION_TITLES = ['结论', '关键事实证据', '运维知识引用', '不确定性与缺失证据', '建议下一步', '受控动作']
const QUESTION = '总结当前集群可引用的运维事实，不创建调查或动作'

async function run() {
  const result = { id: 'IA-ASSISTANT-V3-001', route: '/clusters/:clusterId/assistant', category: 'flows', checks: [], notes: [] }
  const clusterId = ENV.clusterId

  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      await spaNav(page, `/clusters/${clusterId}/assistant`)
      await page.waitForTimeout(800)

      check('no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
      check('no_forbidden_product_copy', !(/(生产平台|生产集群|综合健康分|健康评分|自动修复)/.test(await page.locator('body').innerText())))
      check('scope_is_frozen', await page.getByText('已冻结').first().isVisible().catch(() => false))

      // Compact desktop contract: the answer column keeps the full workspace width
      // and both side panes are hidden behind drawers.
      if (viewport.width <= 1024) {
        const panes = await page.evaluate(() => {
          const sessionPane = document.querySelector('.assistant-workspace > .assistant-session-pane')
          const contextPane = document.querySelector('.assistant-workspace > .assistant-context-panel')
          const mainPane = document.querySelector('.assistant-main-pane')
          const workspace = document.querySelector('.assistant-workspace')
          return {
            sessionDisplay: sessionPane ? getComputedStyle(sessionPane).display : 'missing',
            contextDisplay: contextPane ? getComputedStyle(contextPane).display : 'missing',
            mainWidth: mainPane ? mainPane.getBoundingClientRect().width : 0,
            workspaceWidth: workspace ? workspace.getBoundingClientRect().width : 0,
          }
        })
        check('session_pane_hidden_at_1024', panes.sessionDisplay === 'none', JSON.stringify(panes))
        check('context_pane_hidden_at_1024', panes.contextDisplay === 'none', JSON.stringify(panes))
        check('answer_column_keeps_full_width', panes.workspaceWidth > 0 && panes.mainWidth / panes.workspaceWidth >= 0.95, JSON.stringify(panes))
        // 两个 Drawer 入口必须可用，否则用户无法访问会话与 Scope。
        check('session_drawer_trigger', await page.getByRole('button', { name: '会话' }).first().isVisible().catch(() => false))
        check('context_drawer_trigger', await page.getByRole('button', { name: 'Scope' }).first().isVisible().catch(() => false))
      }

      // Send a read-only diagnostic question and require a structured answer or a
      // explicit, retryable failure. A blank conversation is FAIL.
      const input = page.locator('textarea, input[type="text"]').last()
      const send = page.getByRole('button', { name: '发送' }).first()
      if (!(await input.count()) || !(await send.count())) {
        check('composer_available', false, 'composer or send button missing')
      } else {
        await input.fill(QUESTION)
        await send.click()
        const card = page.locator('[data-testid=assistant-answer-card]').first()
        const failure = page.locator('[role=alert]').first()
        const settled = await Promise.race([
          card.waitFor({ state: 'visible', timeout: 60000 }).then(() => 'answer').catch(() => null),
          failure.waitFor({ state: 'visible', timeout: 60000 }).then(() => 'alert').catch(() => null),
        ])
        check('structured_answer_or_explicit_error', settled === 'answer' || settled === 'alert', `settled=${settled}`)
        if (settled === 'answer') {
          for (const title of SECTION_TITLES) {
            check(`section_${title}`, await card.getByText(title, { exact: true }).first().isVisible().catch(() => false))
          }
          check('no_direct_execution', !(await card.innerText()).match(/立即执行|执行动作|直接执行命令/))
        } else if (settled === 'alert') {
          result.notes.push(`${tag}: AI 服务返回显式错误，记 FAIL 而非把错误当成功`)
          check('structured_answer_required', false, await failure.innerText().catch(() => 'alert'))
        } else {
          check('structured_answer_or_explicit_error', false, 'no answer and no explicit error within timeout')
        }
      }

      await shot(page, `assistant--${tag}--answered`)
    })
  }

  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
