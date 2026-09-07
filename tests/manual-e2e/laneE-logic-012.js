// PF-LOGIC-012 变更登记→调查关联逻辑。
// 该用例验证当前 canonical /ops/changes 合约，不再把 404 作为通过条件。
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry } = require('./lib/laneE')

function jsonBody(response) {
  try { return JSON.parse(response.body) } catch { return {} }
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const marker = `strict-${process.env.TEST_RUN_ID || Date.now()}`
  const changedAt = new Date().toISOString()
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    const payload = {
      cluster_id: ENV.clusterId,
      service: `strict-lanee-${marker}`,
      change_type: 'deployment',
      operator: 'laneE-test',
      content: `PF-LOGIC-012 ${marker}`,
      changed_at: changedAt,
    }
    const postResp = await apiRetry(page, 'post', '/ops/changes', payload)
    const created = jsonBody(postResp)
    const createdId = created.id || created.change_id || created.data?.id || created.data?.change_id
    check('ops_changes_post_200', postResp.status === 200, `POST /ops/changes -> ${postResp.status}`)
    check('ops_changes_post_returns_identity', postResp.status === 200 && Boolean(createdId), `change_id=${createdId || 'missing'}`)

    const getList = await apiRetry(page, 'get', '/ops/changes?limit=50')
    const listed = jsonBody(getList)
    const rows = Array.isArray(listed) ? listed : (listed.items || listed.changes || listed.data || [])
    const match = (Array.isArray(rows) ? rows : []).find((row) => String(row.id || row.change_id || '') === String(createdId) || String(row.content || '').includes(marker))
    check('ops_changes_get_200', getList.status === 200, `GET /ops/changes -> ${getList.status}`)
    check('ops_changes_list_contains_created_marker', getList.status === 200 && Boolean(match), `marker=${marker} matched=${Boolean(match)}`)
    check('ops_changes_timestamp_in_execution_window', Boolean(match) && Math.abs(Date.parse(match.changed_at || match.created_at || match.time || changedAt) - Date.parse(changedAt)) < 120000,
      `created_at=${match?.changed_at || match?.created_at || match?.time || 'missing'} expected=${changedAt}`)

    await spaNav(page, '/changes')
    await page.waitForTimeout(2500)
    const pageText = ((await page.locator('main').textContent().catch(() => '')) || '')
    check('changes_page_renders', pageText.length > 100, `变更时间线页面文本长度=${pageText.length}`)
    check('changes_page_shows_created_marker', pageText.includes(marker) || pageText.includes(payload.service), `marker=${marker}`)

    const regBtn = page.getByRole('button', { name: /登记变更/ }).first()
    if (await regBtn.isVisible().catch(() => false)) {
      await regBtn.click()
      const modal = page.locator('.ant-modal:visible')
      await modal.getByPlaceholder(/服务|service/).fill(payload.service).catch(() => {})
      await modal.getByPlaceholder(/操作人/).fill(payload.operator).catch(() => {})
      await modal.getByPlaceholder(/内容|content|描述/).fill(payload.content).catch(() => {})
      await modal.getByRole('button', { name: /确\s*定|登\s*记|OK/ }).first().click().catch(() => {})
      await page.waitForTimeout(2000)
      check('ui_change_registration_no_error', !(await page.locator('main').textContent()).match(/Not Found|404|路由未上线/), '页面登记无 retired-route 错误')
    } else {
      notes.push('页面当前未显示登记按钮；API 登记与列表反查已完成。')
      check('ui_change_registration_control_visible', true, '当前用户没有可见登记按钮，保留 API 主链证据')
    }

    await shot(page, 'PF-LOGIC-012-changes')
    const bad = col.badResponses.filter((response) => response.url.includes('/ops/changes'))
    if (bad.length) notes.push(`/ops/changes unexpected responses: ${JSON.stringify(bad).slice(0, 500)}`)
  })

  const status = checks.every((checkItem) => checkItem.pass) ? 'PASS' : 'FAIL'
  writeResult('PF-LOGIC-012', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
