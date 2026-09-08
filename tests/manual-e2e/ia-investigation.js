// IA-INVESTIGATION-001: Chat complex question -> formal investigation draft.
// The lane proves the boundary: navigation to the draft never creates a Run;
// mutation is performed only when RUN_E2E_MUTATIONS=1 is explicitly enabled.
const { makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 1280, height: 720 },
  { width: 1024, height: 768 },
]

async function run() {
  const result = { id: 'IA-INVESTIGATION-001', route: '/ai/chat', category: 'flows', checks: [], notes: [] }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      let createRunCalls = 0
      page.on('request', (request) => {
        if (request.method() === 'POST' && /\/api\/v1\/ai\/runs$/.test(request.url())) createRunCalls += 1
      })
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      await spaNav(page, '/ai/chat')
      await page.waitForTimeout(700)
      check('chat_visible', await page.getByText('AI 运维助手').first().isVisible().catch(() => false))
      await spaNav(page, '/investigation/new?source=chat&targetType=service&symptom=分析%20payment-api%20跨服务根因')
      await page.waitForTimeout(500)
      check('draft_visible', await page.getByText('发起 AI 调查').first().isVisible().catch(() => false))
      check('no_implicit_run', createRunCalls === 0, `POST /ai/runs=${createRunCalls}`)
      check('draft_symptom_prefilled', await page.getByLabel('症状 / 调查目标').inputValue().then((value) => value.includes('payment-api')).catch(() => false))
      if (process.env.RUN_E2E_MUTATIONS === '1') {
        await page.getByRole('button', { name: '发起调查' }).click()
        await page.waitForTimeout(500)
        check('one_explicit_run', createRunCalls === 1, `POST /ai/runs=${createRunCalls}`)
      } else {
        result.notes.push(`${tag}: 默认只验证草稿边界；设置 RUN_E2E_MUTATIONS=1 才提交真实 Run`)
      }
      await shot(page, `IA-INVESTIGATION-001-${tag}`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
