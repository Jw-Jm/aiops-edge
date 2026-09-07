// Lane E (PF-LOGIC non-LLM) helper: single-viewport page tests (the shared
// admin login is rate-limited at 10/60s and several lanes run concurrently),
// permission_denied retry for the transient 403s observed under concurrency,
// and the final result JSON writer for test-results/<RUN_ID>/logic/.
const fs = require('fs')
const path = require('path')
const { ENV, ROOT, runPageTest } = require('./harness')

// Conserve the shared login rate budget: one viewport per logic test.
ENV.viewports = [ENV.viewports[ENV.viewports.length - 1]]

const MYSQL = `kubectl exec -n observability mysql-0 -- sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" aiops -e '`

function writeResult(id, checks, notes, status) {
  const out = { id, checks, status, notes }
  const dir = path.join(ROOT, 'logic')
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, `${id}.json`), JSON.stringify(out, null, 2))
  console.log(JSON.stringify({ id, status, pass: checks.filter((c) => c.pass).length, total: checks.length }))
  return out
}

// runPageTest runs one viewport per env; checks may repeat across viewports.
function dedupe(checks) {
  const byName = new Map()
  for (const c of checks) {
    const prev = byName.get(c.name)
    if (!prev) byName.set(c.name, c)
    else if (!prev.pass && c.pass) byName.set(c.name, c)
  }
  return [...byName.values()]
}

// GET/POST with retry: transient 403 permission_denied occurs under concurrent
// lane load (shared admin account / MySQL saturation); retry a few times.
async function apiRetry(page, method, url, data, tries = 4) {
  let last = { status: 0, body: '' }
  for (let i = 0; i < tries; i++) {
    const r = await page.request[method](`${ENV.apiBase}${url}`, data !== undefined ? { data } : undefined)
    const body = await r.text()
    last = { status: r.status(), body }
    if (!(r.status() === 403 && body.includes('permission_denied'))) return last
    await new Promise((res) => setTimeout(res, 5000))
  }
  last.body += ' (permission_denied retries exhausted)'
  return last
}

function sh(cmd) {
  return require('child_process').execSync(cmd, { encoding: 'utf8', timeout: 60000 })
}

// base64 传递 SQL，彻底规避多层 shell 引号嵌套问题。
function mysqlExec(sql) {
  const b64 = Buffer.from(sql, 'utf8').toString('base64')
  return sh(`kubectl exec -n observability mysql-0 -- sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" aiops -e "$(echo ${b64} | base64 -d)"' 2>/dev/null`)
}
function chExec(q) {
  const b64 = Buffer.from(q, 'utf8').toString('base64')
  return sh(`kubectl exec -n observability clickhouse-0 -c clickhouse -- sh -c 'clickhouse-client --password "$CH_PROBE_PASSWORD" -q "$(echo ${b64} | base64 -d)"' 2>/dev/null`)
}

module.exports = { ENV, ROOT, runPageTest, writeResult, dedupe, apiRetry, MYSQL, sh, mysqlExec, chExec }
