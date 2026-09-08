// IA-ACTIONS-001: Run action -> action center audit drawer.
// Approval is intentionally opt-in so a visual/contract check never mutates a
// production action. RUN_E2E_MUTATIONS=1 enables the explicit approval branch.
const { makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 1280, height: 720 },
  { width: 1024, height: 768 },
]

async function run() {
  const result = { id: 'IA-ACTIONS-001', route: '/actions', category: 'flows', checks: [], notes: [] }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      await spaNav(page, '/actions')
      await page.waitForTimeout(800)
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      check('action_center_visible', await page.getByTestId('action-center').isVisible().catch(() => false))
      const lifecycleLabelsVisible = await Promise.all(['待审批', '待执行', '执行中', '待验证', '已完成/失败'].map((label) => page.getByText(new RegExp(label)).count().then((count) => count > 0).catch(() => false)))
      check('lifecycle_tabs_visible', lifecycleLabelsVisible.every(Boolean))
      const detailButton = page.getByRole('button', { name: '查看详情' }).first()
      if (await detailButton.count()) {
        await detailButton.click()
        await page.waitForTimeout(300)
        check('resource_version_visible', await page.getByText(/ResourceVersion/).isVisible().catch(() => false))
        check('preflight_visible', await page.getByText('预检').isVisible().catch(() => false))
        check('source_run_visible', await page.getByText(/来源 Run/).isVisible().catch(() => false))
        if (process.env.RUN_E2E_MUTATIONS === '1' && await page.getByRole('button', { name: '批准执行' }).count()) {
          await page.getByRole('button', { name: '批准执行' }).click()
          await page.getByRole('button', { name: '确认' }).click()
          check('approval_confirmation_submitted', true)
        } else {
          result.notes.push(`${tag}: 默认只读检查动作审计字段；设置 RUN_E2E_MUTATIONS=1 才批准真实动作`)
        }
      } else {
        check('empty_action_state_is_explicit', await page.getByText('暂无动作').isVisible().catch(() => false))
        result.notes.push(`${tag}: 当前作用域没有待审动作，未伪造动作记录`)
      }
      await shot(page, `IA-ACTIONS-001-${tag}`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
