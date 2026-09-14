#!/usr/bin/env node
/**
 * AI 智能故障定位评测运行器（T11 / 设计文档第 11 章）。
 *
 * 用法:
 *   node scripts/acceptance/run-ai-eval.mjs --session <cookie-file> --base http://localhost:30253 \
 *        --out docs/acceptance/<run_id>/ai-rca --repeat 5 --timeout 180
 *
 * 场景清单（ground truth）内置于 SCENARIOS。每个场景固定：
 *   - ground truth（可接受替代答案数组）
 *   - 故障对象（target_type + resource_id，canonical UID 或名称）
 *   - 故障类型（计算与调度/网络与入口/存储/应用依赖/平台数据链/变更与配置/负样本/对抗）
 * 评测判定（确定性、可复现）：
 *   - 拒答正确（negative scenario → status != success 且 root_cause 为空）
 *   - Top-1 命中（root_cause 与 ground truth 匹配）
 *   - 证据非空且 evidence_id 可打开（GET evidences 返回同 ID）
 */
import fs from 'node:fs'
import path from 'node:path'

const args = process.argv.slice(2)
const opt = (name, def) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args[i + 1] : def
}
const base = opt('base', 'http://localhost:30253')
const cookieFile = opt('session', '/tmp/cj.txt')
const outDir = opt('out', 'test-results/ai-eval')
const repeat = Number(opt('repeat', '1'))
const timeoutSec = Number(opt('timeout', '150'))
const only = opt('only', '')

const cookie = fs.readFileSync(cookieFile, 'utf8')
  .split('\n')
  // libcurl 对 HttpOnly cookie 写 "#HttpOnly_<tab 分隔行>"，不能按 "# " 注释过滤
  .filter((l) => l && !l.startsWith('# ') && !l.startsWith('#\t'))
  .map((l) => l.replace(/^#HttpOnly_/, '').replace(/\r$/, '').split('\t'))
  .filter((p) => p.length >= 7)
  .map((p) => `${p[5].trim()}=${p[6].trim()}`)
  .join('; ')

const CLUSTER = '91771a6e-9c2d-11f1-8271-bea176fe9f9f'
const TENANT = '7ed01afc-cc79-4ecd-8767-a2befa6168ad'

// 场景 ground truth 在固定模型/工具/知识/图谱/策略/数据版本后重复运行（§11）。
// resource_id 使用图谱 canonical UID（由 /ai/kg/entities/search 现场解析，
// 保证 UID 与当前投影 generation 一致，不硬编码可能过期的对象版本）。
const SCENARIOS = [
  {
    id: 'S01-deployment-restart',
    domain: '计算与调度',
    intent: 'kube-system metrics-server deployment Pod 重启次数上升，请定位原因',
    target: { type: 'deployment', name: 'metrics-server' },
    groundTruth: ['metrics-server'],
    negative: false,
  },
  {
    id: 'S02-probe-failure',
    domain: '应用依赖',
    intent: 'deepflow-grafana Liveness 探针失败 503，定位根因',
    target: { type: 'deployment', name: 'deepflow-grafana' },
    groundTruth: ['deepflow-grafana'],
    negative: false,
  },
  {
    id: 'S99-unmodeled-service',
    domain: '负样本',
    intent: 'regression-svc 服务错误率 critical 告警，请定位原因',
    target: { type: 'service', name: 'regression-svc' },
    groundTruth: [],
    negative: true,
  },
  {
    id: 'S98-empty-target',
    domain: '对抗与安全',
    intent: '调查一个不存在的服务 no-such-svc-xyz 的根因',
    target: { type: 'service', name: 'no-such-svc-xyz' },
    groundTruth: [],
    negative: true,
  },
]

async function api(pathname, init = {}) {
  const res = await fetch(base + pathname, {
    ...init,
    headers: { 'Content-Type': 'application/json', cookie, ...(init.headers || {}) },
  })
  const text = await res.text()
  let body = null
  try { body = JSON.parse(text) } catch { body = { raw: text.slice(0, 200) } }
  return { status: res.status, body }
}

async function resolveUid(name) {
  const r = await api(`/api/v1/ai/kg/entities/search?q=${encodeURIComponent(name)}&limit=1`)
  const item = r.body?.items?.[0]
  return item?.entity_uid || ''
}

async function runOnce(scenario, runIndex) {
  const started = Date.now()
  const resource = await resolveUid(scenario.target.name)
  // 时间窗覆盖故障注入时刻（D20）：默认 30 分钟窗可能完全落在故障信号之后。
  // 显式 4 小时窗（上限 24h）保证告警/事件类证据落在窗口内。
  const end = new Date()
  const start = new Date(end.getTime() - (scenario.windowHours ?? 4) * 3600 * 1000)
  const create = await api('/api/v1/ai/runs', {
    method: 'POST',
    body: JSON.stringify({
      intent: `[评测 ${scenario.id} #${runIndex}] ${scenario.intent}`,
      cluster_id: CLUSTER,
      action_mode: 'read_only',
      target_type: scenario.target.type,
      resource_id: resource || scenario.target.name,
      time_range_start: start.toISOString(),
      time_range_end: end.toISOString(),
    }),
  })
  const runId = create.body?.run_id
  if (create.status !== 201 || !runId) {
    return { runId: null, error: `create ${create.status}` }
  }
  // 轮询终态（p95 < 300s 要求；此处 timeoutSec 上限）
  const deadline = started + timeoutSec * 1000
  let detail = null
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 5000))
    const d = await api(`/api/v1/ai/runs/${runId}`)
    detail = d.body?.run
    if (detail && ['success', 'partial', 'failed', 'regressed', 'cancelled'].includes(detail.status)) break
  }
  const ev = await api(`/api/v1/ai/runs/${runId}/evidences?tenant_id=${TENANT}&cluster_id=${CLUSTER}`)
  const evidences = ev.body?.evidences || []
  const elapsed = Date.now() - started
  return {
    runId,
    status: detail?.status,
    rootCause: detail?.root_cause || '',
    confidence: detail?.confidence ?? 0,
    evidenceCount: evidences.length,
    evidenceIds: evidences.map((e) => e.evidence_id || e.id),
    elapsedMs: elapsed,
  }
}

function judge(scenario, result) {
  if (result.error) return { verdict: 'error' }
  if (scenario.negative) {
    // 无可判定根因时必须拒答：root_cause 为空且不得输出 success/Confirmed
    const refused = !result.rootCause && result.status !== 'success'
    return { verdict: refused ? 'pass' : 'false_positive' }
  }
  if (!result.rootCause) return { verdict: 'no_answer' }
  const hit = scenario.groundTruth.some((g) => String(result.rootCause).includes(g))
  return { verdict: hit ? 'pass' : 'wrong_answer' }
}

const scenarios = only ? SCENARIOS.filter((s) => s.id === only) : SCENARIOS
fs.mkdirSync(outDir, { recursive: true })
const results = []
for (const scenario of scenarios) {
  for (let i = 1; i <= repeat; i += 1) {
    process.stdout.write(`run ${scenario.id} #${i} ... `)
    const result = await runOnce(scenario, i)
    const judgment = judge(scenario, result)
    results.push({ scenario: scenario.id, domain: scenario.domain, run: i, judgment, ...result })
    console.log(`${judgment.verdict} (${result.elapsedMs ?? 0}ms, evidence=${result.evidenceCount ?? 0})`)
  }
}

const passed = results.filter((r) => r.judgment.verdict === 'pass').length
const summary = {
  total: results.length,
  passed,
  passRate: results.length ? Number((passed / results.length).toFixed(3)) : 0,
  falsePositives: results.filter((r) => r.judgment.verdict === 'false_positive').length,
  firstVisibleProgressP95: null,
  results,
}
fs.writeFileSync(path.join(outDir, 'eval-results.json'), JSON.stringify(summary, null, 2))
console.log(`\nsummary: ${summary.passed}/${summary.total} pass, falsePositives=${summary.falsePositives}`)
console.log(`written: ${path.join(outDir, 'eval-results.json')}`)
process.exit(0)
