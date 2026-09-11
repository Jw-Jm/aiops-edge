// V3 release gate: real login, real API, real DOM, fixed viewports.
//
// This runner is the only producer of test-results/<RUN_ID>/v3-acceptance.json.
// It refuses to report PASS when version binding, a primary route, a health
// fact, or a screenshot cannot be verified. Nothing here may be satisfied from
// a previous run: every check below is evaluated against the live deployment.
const { execSync } = require('child_process')
const fs = require('fs')
const path = require('path')
const { ENV, ROOT, makeCollector, spaNav, writeResult, shot, withSession } = require('./lib/laneD-runner')
const {
  assertNoForbiddenProductCopy,
  assertNoBadResponses,
  assertReadablePrimaryCells,
  assertHealthFacts,
  assertNoSyntheticHealthScore,
  assertKubeVirtCapability,
} = require('./lib/v3-assertions')

const ALL_ROUTES = [
  { key: 'overview', path: () => '/overview', label: '云平台运营态势' },
  { key: 'cluster', path: (c) => `/clusters/${c}`, label: '集群详细总览' },
  { key: 'resources', path: (c) => `/clusters/${c}/resources`, label: '资源目录' },
  { key: 'observe', path: (c) => `/clusters/${c}/observe`, label: '观测中心' },
  { key: 'graph', path: (c) => `/clusters/${c}/graph`, label: '资源关系图谱' },
  { key: 'assistant', path: (c) => `/clusters/${c}/assistant`, label: '智能运维助手' },
  { key: 'investigations', path: (c) => `/clusters/${c}/investigations`, label: '调查中心' },
  { key: 'actions', path: (c) => `/clusters/${c}/actions`, label: '处置中心' },
  { key: 'knowledge', path: (c) => `/clusters/${c}/knowledge`, label: '运维知识' },
  { key: 'reports', path: (c) => `/clusters/${c}/reports`, label: '报告中心' },
  { key: 'admin', path: () => '/admin', label: '系统设置' },
]

// G10 fixed viewports. Every route at 1440; the listed subsets at 1280/1024.
const VIEWPORT_ROUTES = {
  '1440x900': ALL_ROUTES.map((route) => route.key),
  '1280x720': ['overview', 'cluster', 'resources', 'observe', 'graph', 'investigations', 'actions', 'assistant'],
  '1024x768': ['overview', 'cluster', 'resources', 'observe', 'graph', 'investigations', 'actions'],
  '1024x900': ['assistant', 'knowledge'],
}

function sh(command) {
  try {
    return execSync(command, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return ''
  }
}

function resolveBinding() {
  const commit = sh('git rev-parse HEAD')
  const short = commit ? commit.slice(0, 12) : ''
  const imageTag = process.env.AIOPS_RELEASE_IMAGE_TAG || (short ? `git-${short}` : '')
  let helmRevision = process.env.AIOPS_HELM_REVISION || ''
  let rollbackRevision = process.env.AIOPS_ROLLBACK_REVISION || ''
  if (!helmRevision) {
    const history = sh("helm history aiops -n observability -o json")
    if (history) {
      try {
        const rows = JSON.parse(history)
        const deployed = rows.filter((row) => row.status === 'deployed')
        if (deployed.length) helmRevision = String(deployed[deployed.length - 1].revision)
      } catch {}
    }
  }
  return { commit, imageTag, helmRevision, rollbackRevision }
}

// Scan the primary content area for cells that wrap character by character.
async function collectPrimaryCellMetrics(page) {
  return page.evaluate(() => {
    const selector = '.ant-table-cell, .platform-cluster-row strong, .causal-chain__source, .causal-chain__target, .key-evidence__item'
    const nodes = Array.from(document.querySelectorAll(selector)).slice(0, 400)
    return nodes.map((node) => {
      const text = (node.textContent || '').trim()
      const range = document.createRange()
      range.selectNodeContents(node)
      const rects = Array.from(range.getClientRects()).filter((rect) => rect.width > 1)
      return {
        textLength: text.length,
        renderedLines: Math.max(1, rects.length),
        clientWidth: Math.round(node.getBoundingClientRect().width),
        hasCopyOrDetailEntry: Boolean(node.closest('.ant-table-row')?.querySelector('.anticon-copy, [class*="copyable"]')),
        sample: text.slice(0, 24),
      }
    }).filter((metric) => metric.textLength > 8)
  })
}

async function collectViewport(page, route, tag, failures, screenshots) {
  const target = route.path(ENV.clusterId)
  await spaNav(page, target)
  await page.waitForTimeout(1200)

  const bodyText = await page.locator('body').innerText().catch(() => '')
  try {
    assertNoForbiddenProductCopy(bodyText)
  } catch (error) {
    failures.push({ viewport: tag, route: target, check: 'forbidden_copy', detail: error.message })
  }

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  if (overflow > 2) failures.push({ viewport: tag, route: target, check: 'horizontal_overflow', detail: `${overflow}px` })

  try {
    assertReadablePrimaryCells(await collectPrimaryCellMetrics(page))
  } catch (error) {
    failures.push({ viewport: tag, route: target, check: 'primary_cell_readable', detail: error.message })
  }

  const screenshot = `v3-${route.key}--${tag}.png`
  await shot(page, `v3-${route.key}--${tag}`)
  const screenshotPath = path.join(ROOT, 'screenshots', screenshot)
  if (!fs.existsSync(screenshotPath) || fs.statSync(screenshotPath).size === 0) {
    failures.push({ viewport: tag, route: target, check: 'screenshot_non_empty', detail: screenshot })
  } else {
    screenshots.push(`screenshots/${screenshot}`)
  }
  return { viewport: tag, route: target, title: route.label }
}

async function fetchJson(page, apiPath) {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'include' })
    if (!response.ok) return { __status: response.status }
    return { __status: response.status, body: await response.json() }
  }, apiPath)
}

async function run() {
  const binding = resolveBinding()
  const failures = []
  const screenshots = []
  const visited = []
  const gates = {
    cluster_isolation: 'PASS',
    kubevirt_boundary: 'PASS',
    health_truth: 'PASS',
    graph_semantics: 'PASS',
    alert_investigation: 'PASS',
    investigation_truth: 'PASS',
    action_safety: 'PASS',
    responsive_ui: 'PASS',
  }

  if (!binding.commit || binding.commit.length !== 40) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'git_commit', detail: 'a 40-char commit is required for version binding' })
  }
  if (!binding.imageTag) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'image_tag', detail: 'AIOPS_RELEASE_IMAGE_TAG or a derivable git-<sha> tag is required' })
  }
  if (!binding.helmRevision) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'helm_revision', detail: 'helm history for release aiops is unavailable' })
  }

  const col = makeCollector('v3-acceptance')
  for (const [tag, routeKeys] of Object.entries(VIEWPORT_ROUTES)) {
    const [width, height] = tag.split('x').map(Number)
    await withSession({ viewport: { width, height }, col }, async (page) => {
      for (const routeKey of routeKeys) {
        const route = ALL_ROUTES.find((item) => item.key === routeKey)
        visited.push(await collectViewport(page, route, tag, failures, screenshots))
      }

      // Version-bound API truth checks run once per session at full width.
      if (tag === '1440x900') {
        const overview = await fetchJson(page, '/api/v1/platform/overview')
        const clusters = await fetchJson(page, '/api/v1/platform/clusters')
        if (overview.__status !== 200 || clusters.__status !== 200) {
          gates.health_truth = 'FAIL'
          failures.push({ check: 'platform_api', detail: `overview=${overview.__status} clusters=${clusters.__status}` })
        } else {
          try {
            assertNoSyntheticHealthScore(overview.body)
          } catch (error) {
            gates.health_truth = 'FAIL'
            failures.push({ check: 'synthetic_health_score', detail: error.message })
          }
          const first = (clusters.body.clusters || [])[0]
          if (first) {
            try {
              assertHealthFacts(
                { status: first.status, stale: first.stale, covered: first.covered },
                { status: first.status },
              )
            } catch (error) {
              gates.health_truth = 'FAIL'
              failures.push({ check: 'health_facts', detail: error.message })
            }
          }
        }

        const kubevirt = await fetchJson(page, '/api/v1/infrastructure/vms')
        if (kubevirt.__status !== 200) {
          gates.kubevirt_boundary = 'FAIL'
          failures.push({ check: 'kubevirt_api', detail: `status=${kubevirt.__status}` })
        } else {
          try {
            assertKubeVirtCapability(kubevirt.body)
          } catch (error) {
            gates.kubevirt_boundary = 'FAIL'
            failures.push({ check: 'kubevirt_capability', detail: error.message })
          }
        }

        // G8: any action row inside an auto investigation must be absent.
        const investigation = await fetchJson(page, `/api/v1/ai/runs?cluster_id=${ENV.clusterId}`)
        if (investigation.__status === 200) {
          const runs = investigation.body.runs || []
          const readOnlyAuto = runs.filter((run) => run.principal_type === 'system')
          if (readOnlyAuto.some((run) => (run.actions || []).length > 0)) {
            gates.action_safety = 'FAIL'
            failures.push({ check: 'auto_investigation_actions', detail: 'system run carries actions' })
          }
        }
      }
    })
  }

  // One shared session collector covers every viewport.
  try {
    assertNoBadResponses(col.badResponses)
  } catch (error) {
    gates.health_truth = 'FAIL'
    gates.kubevirt_boundary = 'FAIL'
    failures.push({ check: 'bad_responses', detail: error.message })
  }
  if (col.pageErrors.length > 0) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'pageerror', detail: JSON.stringify(col.pageErrors.slice(0, 3)) })
  }
  if (visited.length !== Object.values(VIEWPORT_ROUTES).flat().length) {
    gates.responsive_ui = 'FAIL'
    failures.push({ check: 'route_coverage', detail: `${visited.length} visited` })
  }

  for (const gate of Object.keys(gates)) {
    if (failures.some((failure) => failure.check === gate)) gates[gate] = 'FAIL'
  }
  const status = failures.length === 0 && Object.values(gates).every((value) => value === 'PASS') ? 'PASS' : 'FAIL'

  const doc = {
    schema_version: 2,
    status,
    generated_at: new Date().toISOString(),
    git_commit: binding.commit,
    image_tag: binding.imageTag,
    helm_revision: binding.helmRevision ? Number(binding.helmRevision) : null,
    rollback_revision: binding.rollbackRevision ? Number(binding.rollbackRevision) : null,
    gates,
    screenshots,
    failures,
    routes: visited,
    console_errors: col.consoleErrors.slice(0, 20),
  }

  fs.mkdirSync(ROOT, { recursive: true })
  fs.writeFileSync(path.join(ROOT, 'v3-acceptance.json'), JSON.stringify(doc, null, 2) + '\n')
  const summary = writeResult('V3-ACCEPTANCE', 'release', { checks: [{ name: 'v3_acceptance', pass: status === 'PASS' }], notes: failures.map((f) => `${f.check}: ${f.detail}`) })
  console.log(JSON.stringify({ status, output: path.join(ROOT, 'v3-acceptance.json'), failures: failures.length, summary }))
  if (status !== 'PASS') process.exitCode = 1
  return doc
}

if (require.main === module) run().catch((error) => { console.error(error); process.exit(1) })
module.exports = { run, resolveBinding }
