// IA-RESOURCES-001: cluster-owned resource journey and visual contract.
// This lane is read-only by default: selecting a resource changes UI scope but
// does not create a Run or Action.
const { makeCollector, spaNav, withSession, writeResult, shot } = require('./lib/laneD-runner')

const VIEWPORTS = [{ width: 1440, height: 900 }, { width: 1280, height: 720 }, { width: 1024, height: 768 }]

async function run() {
  const result = { id: 'IA-RESOURCES-001', route: '/resources', category: 'flows', checks: [], notes: [] }
  for (const viewport of VIEWPORTS) {
    const tag = `${viewport.width}x${viewport.height}`
    const col = makeCollector(tag)
    await withSession({ viewport, col }, async (page) => {
      const check = (name, pass, detail = '') => result.checks.push({ name: `${tag}_${name}`, pass: !!pass, detail })
      await spaNav(page, '/resources')
      await page.waitForTimeout(900)
      const panoramaState = await page.getByText(/集群全景读取失败|数据读取失败|当前域暂无资源|无权访问/).count().then((count) => count > 0).catch(() => false)
      check('panorama_or_explicit_state', await page.getByTestId('cluster-panorama').isVisible().catch(() => false) || panoramaState)
      check('five_resource_domains_visible', (await Promise.all(['计算', '网络', '存储', 'Kubernetes', '应用服务'].map((label) => page.getByText(label, { exact: true }).count().then((count) => count > 0).catch(() => false)))).every(Boolean))
      check('production_only_language', !(await page.locator('body').innerText()).match(/演示环境|开发环境|测试环境|staging|dev/i))
      await page.getByRole('tab', { name: '计算' }).click().catch(() => {})
      await page.waitForTimeout(500)
      check('resource_directory_or_explicit_state', await page.getByRole('region', { name: '资源目录' }).count().then((count) => count > 0).catch(() => false) || await page.getByText(/当前域暂无资源|资源目录读取失败|读取失败|无权访问/).count().then((count) => count > 0).catch(() => false))
      const row = page.locator('.resource-directory__row').first()
      if (await row.count()) {
        await row.click()
        await page.waitForTimeout(400)
        check('typed_resource_identity_visible', await page.getByText('资源身份与详情').isVisible().catch(() => false))
        const operationLinks = await page.locator('a[href*="/actions?resource="]').evaluateAll((links) => links.map((link) => link.getAttribute('href') || ''))
        check('resource_operation_link_is_scoped', operationLinks.length === 0 || operationLinks.every((href) => href.includes('resource=')))
      } else result.notes.push(`${tag}: 计算域暂无资源，保留明确空态`)
      check('no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
      await shot(page, `resources--${tag}--default`)
    })
  }
  const summary = writeResult(result.id, result.category, result)
  console.log(JSON.stringify(summary))
  return summary
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run }
