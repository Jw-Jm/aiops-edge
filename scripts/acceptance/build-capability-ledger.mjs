#!/usr/bin/env node
/**
 * 能力覆盖账本生成器（规范 §8.4 / 附录 C）
 *
 * 数据来源（全部来自真实代码，不使用手写清单）：
 *   1. backend  : ai-apm-query-go/internal/bootstrap/http.go 中注册的全部 HTTP 路由
 *   2. frontend : observability-frontend/src/api/** 中真实发起的请求路径
 *
 * 输出：capability-map.csv（capability_id,interface_id,transport,producer,consumer,
 *        classification,ui_route,ui_entry,scope,risk,status_coverage,test_id,
 *        evidence,owner,gap,status）
 *
 * 用法：node scripts/acceptance/build-capability-ledger.mjs [--out <path>]
 */
import { readFileSync, writeFileSync, readdirSync, mkdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(__dirname, '../..')
const QUERY_GO_HTTP = join(REPO, 'ai-apm-query-go/internal/bootstrap/http.go')
const FE_ROOT = join(REPO, 'observability-frontend')
const FE_API_DIR = join(FE_ROOT, 'src/api')

const outArgIdx = process.argv.indexOf('--out')
const OUT = outArgIdx > -1 ? resolve(process.argv[outArgIdx + 1]) : join(REPO, 'docs/acceptance/current/capability-map.csv')

/** 八大一级页面（规范 §4.1），顺序固定 */
export const PRIMARY_PAGES = [
  { id: 'ai-operations', label: 'AI 智能运维', route: '/ai-operations' },
  { id: 'overview', label: '总览', route: '/overview' },
  { id: 'clusters', label: '集群', route: '/clusters/:clusterUid/overview' },
  { id: 'observability', label: '全链路监控', route: '/observability' },
  { id: 'knowledge-graph', label: '知识图谱', route: '/knowledge-graph' },
  { id: 'knowledge', label: '知识库', route: '/knowledge' },
  { id: 'reports', label: '报告', route: '/reports' },
  { id: 'settings', label: '设置', route: '/settings' },
]

/**
 * 分类规则（规范 §8.1）：
 *   A 页面     八字页面承载的完整用户任务
 *   B 上下文   嵌入 A 类页面的上下文对象（告警/资源/证据/动作记录）
 *   C 设置分区 低频治理配置
 *   D API-only 服务间内部能力
 */
const RULES = [
  // D：服务间内部能力，不得由浏览器调用
  [/^\/(internal|livez|readyz|health|metrics|enable)$/, 'D', null, 'internal'],
  // 任意 PromQL range 直通已按设计关闭（返回 METRICS_PROMQL_RANGE_DISABLED），
  // 不是可消费的公共能力；typed metrics contract 才是。
  [/^\/api\/v1\/metrics\/query_range$/, 'D', null, 'internal'],
  [/^\/api\/v1\/(health|livez|readyz|metrics)$/, 'D', null, 'internal'],
  // 浏览器直连的受控终端 WebSocket：按设计不暴露给浏览器（AI 不持有基础设施 shell），
  // 保留为内部能力并由安全分区的只读能力核查暴露可观测性。
  [/^\/api\/v1\/shell\/ws$/, 'D', null, 'internal'],
  [/^\/internal\//, 'D', null, 'internal'],
  // C：设置分区
  [/^\/api\/v1\/settings\/llm/, 'C', '/settings?section=llm', 'llm'],
  [/^\/api\/v1\/settings\/k8s/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/system\//, 'C', '/settings?section=health', 'health'],
  [/^\/api\/v1\/tenants/, 'C', '/settings?section=security', 'security'],
  [/^\/api\/v1\/users/, 'C', '/settings?section=security', 'security'],
  // 破坏性清理执行端点必须先于 C 类规则匹配：按设计不由浏览器调用
  [/^\/api\/v1\/admin\/data-cleanups\/execute$/, 'D', null, 'internal'],
  [/^\/api\/v1\/admin\/data-cleanups/, 'C', '/settings?section=health', 'health'],
  [/^\/api\/v1\/ai\/(skills|agents|flows|workflows)/, 'C', '/settings?section=workflow', 'workflow'],
  [/^\/api\/v1\/ai\/rules/, 'C', '/settings?section=security', 'security'],
  [/^\/api\/v1\/mcp\//, 'C', '/settings?section=mcp', 'mcp'],
  [/^\/api\/v1\/ai\/knowledge/, 'C', '/settings?section=rag', 'rag'],
  [/^\/api\/v1\/ai\/kg\/ops/, 'C', '/settings?section=graph', 'graph'],
  [/^\/api\/v1\/topology\/(sync|sync-catalog|node-types|relation-types)/, 'C', '/settings?section=graph', 'graph'],
  [/^\/api\/v1\/clusters\/(\{clusterId\}|\/)/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/devices/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/slo/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/alerts\/(rules|silences)/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/data\/sync/, 'C', '/settings?section=integrations', 'integrations'],
  [/^\/api\/v1\/deepflow\/status/, 'C', '/settings?section=health', 'health'],
  // 智能运维只读终端检查：安全治理，设置·安全分区呈现
  [/^\/api\/v1\/ai\/shell\/check$/, 'C', '/settings?section=security', 'security'],
  // 网络设备（SNMP）：配置入口在设置·接入，设备明细在集群上下文
  [/^\/api\/v1\/snmp/, 'C', '/settings?section=integrations', 'integrations'],
  // A：八大级页面主体
  [/^\/api\/v1\/ai\/(runs|session|sessions|chat|suggestion|nl2sql|final_report)/, 'A', '/ai-operations', 'ai-operations'],
  [/^\/api\/v1\/platform\/(overview|clusters|capacity)$/, 'A', '/overview', 'overview'],
  [/^\/api\/v1\/observability\/paths/, 'A', '/observability', 'observability'],
  [/^\/api\/v1\/clusters\/?$/, 'C', '/settings?section=integrations', 'integrations'],

  [/^\/api\/v1\/(dashboard|capacity)\//, 'A', '/overview', 'overview'],
  [/^\/api\/v1\/clusters\/\{clusterId\}\/overview/, 'A', '/clusters/:clusterUid/overview', 'clusters'],
  [/^\/api\/v1\/node/, 'A', '/clusters/:clusterUid/overview?section=compute', 'clusters'],
  [/^\/api\/v1\/infrastructure\/nodes/, 'A', '/clusters/:clusterUid/overview?section=compute', 'clusters'],
  [/^\/api\/v1\/ipmi/, 'A', '/clusters/:clusterUid/overview?section=compute', 'clusters'],
  [/^\/api\/v1\/graph_public|^\/api\/v1\/ai\/kg/, 'A', '/knowledge-graph', 'knowledge-graph'],
  [/^\/api\/v1\/clusters\/\{clusterId\}\/knowledge/, 'A', '/knowledge', 'knowledge'],
  [/^\/api\/v1\/ops\/reports/, 'A', '/reports', 'reports'],
  // B：上下文对象（告警/资源/证据/动作）
  [/^\/api\/v1\/(alerts|anomaly)/, 'B', '/overview', 'alerts'],
  // 智能运维终端（WebSocket）属于 AI 智能运维任务内的受控工具
  [/^\/api\/v1\/shell\/ws$/, 'B', '/ai-operations', 'ai-operations'],
  [/^\/api\/v1\/admin\/data-cleanups/, 'C', '/settings?section=health', 'health'],
  [/^\/api\/v1\/(tenants|users)/, 'C', '/settings?section=security', 'security'],
  [/^\/api\/v1\/ai\/actions/, 'B', '/ai-operations', 'actions'],
  [/^\/api\/v1\/(services|resources|catalog|topology)/, 'B', '/clusters/:clusterUid/overview?tab=resources', 'resources'],
  [/^\/api\/v1\/(metrics|logs|traces)/, 'B', '/observability', 'telemetry'],
  [/^\/api\/v1\/infrastructure\/(pods|deployments|hpa|namespaces|vms)/, 'B', '/clusters/:clusterUid/overview?tab=resources', 'resources'],
  [/^\/api\/v1\/ops\//, 'B', '/ai-operations', 'actions'],
  [/^\/api\/v1\/shell\/ws/, 'B', '/ai-operations', 'actions'],
  [/^\/api\/v1\/auth|^\/api\/v1\/login|^\/api\/v1\/me/, 'B', 'topbar-account', 'account'],
]

function classify(route) {
  for (const [re, cls, uiRoute, domain] of RULES) {
    if (re.test(route)) return { cls, uiRoute, domain }
  }
  return { cls: 'UNCLASSIFIED', uiRoute: null, domain: 'unknown' }
}

/**
 * 去掉注释后再扫描：注释里出现的路径不是消费点。
 * 否则"说明某端点已关闭"的注释会被误判为前端在调用它。
 */
function stripComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

function backendRoutes() {
  const src = readFileSync(QUERY_GO_HTTP, 'utf8')
  const found = new Set()
  const re = /"(\/(?:api\/v1|internal\/v1|health|livez|readyz|metrics|enable)[^"]*)"/g
  let m
  while ((m = re.exec(src)) !== null) found.add(m[1])
  return [...found].sort()
}

function frontendPaths() {
  const found = new Set()
  // 1) src/api/** 的 axios 字面量路径
  const apiFiles = readdirSync(FE_API_DIR).filter((f) => f.endsWith('.ts') && !f.endsWith('.test.ts') && !f.includes('fixtures'))
  // 注意：模板字面量 `${fn(x)}` 含括号，字符类必须放宽，否则会漏统计
  // 真实的 UI 消费点（曾导致 /topology/node/ 被误报为孤儿能力）。
  const collect = (src) => {
    const re = /[`'"](\/[^`'"\s]+)[`'"]/g
    let m
    while ((m = re.exec(src)) !== null) {
      let p = m[1]
      if (p === '/api/v1' || p === '/') continue
      p = p.replace(/\$\{[^}]*\}/g, ':id')
      found.add(p.startsWith('/api/v1') ? p : `/api/v1${p}`)
    }
  }
  for (const f of apiFiles) collect(stripComments(readFileSync(join(FE_API_DIR, f), 'utf8')))

  // 2) 页面/特性层直接发起的请求：api.get('...') / api.post('...') / api.put('...') / api.delete('...')
  // 以及设置页分区绑定表里的 path: '...'（这是真实的 UI 消费点，不能因为写在页面里就漏统计）
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name)
      if (entry.isDirectory()) {
        walk(full)
        continue
      }
      if (!/\.(ts|tsx)$/.test(entry.name) || /\.test\.(ts|tsx)$/.test(entry.name) || entry.name.includes('fixtures')) continue
      const src = stripComments(readFileSync(full, 'utf8'))
      const rel = full.slice(FE_ROOT.length)
      const apiCall = /api\.(?:get|post|put|patch|delete)(?:<[^>]*>)?\(\s*[`'"](\/[a-zA-Z0-9/_.:{}$-]+)[`'"]/g
      let m
      while ((m = apiCall.exec(src)) !== null) {
        const p = m[1].replace(/\$\{[^}]*\}/g, ':id')
        if (p !== '/') found.add(p.startsWith('/api/v1') ? p : `/api/v1${p}`)
      }
      if (rel.includes('/pages/settings/')) {
        const declared = /path:\s*[`'"](\/[a-zA-Z0-9/_.:{}$-]+)[`'"]/g
        while ((m = declared.exec(src)) !== null) {
          const p = m[1].replace(/\$\{[^}]*\}/g, ':id')
          found.add(p.startsWith('/api/v1') ? p : `/api/v1${p}`)
        }
      }
    }
  }
  walk(join(FE_ROOT, 'src'))
  return found
}

function normalize(route) {
  return route
    .replace(/\{(\w+)\}/g, ':id')
    .replace(/:id/g, ':id')
    .replace(/\/$/, '')
}

function main() {
  const routes = backendRoutes()
  const consumed = frontendPaths()
  const consumedNorm = new Set([...consumed].map(normalize))

  const rows = [
    'capability_id,interface_id,transport,producer,consumer,classification,ui_route,ui_entry,scope,risk,status_coverage,test_id,evidence,owner,gap,status',
  ]

  let n = 0
  for (const route of routes) {
    n += 1
    const { cls, uiRoute, domain } = classify(route)
    const norm = normalize(route)
    // 前端消费判定：精确匹配，或前端路径是后端路径的前缀（子资源）
    let feConsumer = ''
    for (const c of consumedNorm) {
      if (c === norm || norm.startsWith(`${c}/`) || c.startsWith(`${norm}/`)) {
        feConsumer = c
        break
      }
    }
    const hasFe = Boolean(feConsumer)
    const gap =
      cls === 'D'
        ? (hasFe ? 'INTERNAL_MUST_NOT_BE_CALLED_FROM_BROWSER' : '')
        : hasFe
          ? ''
          : 'NO_FRONTEND_CONSUMER'
    const status = gap ? 'GAP' : cls === 'D' ? 'API_ONLY_OK' : 'MAPPED'
    const id = `CAP-${String(n).padStart(4, '0')}`
    rows.push(
      [
        id,
        route,
        'REST',
        'query-api',
        feConsumer || 'none',
        cls,
        uiRoute ?? '',
        '',
        'tenant+cluster',
        cls === 'D' ? 'internal' : 'read',
        'loading|success|empty|partial|stale|failed|forbidden',
        '',
        '',
        domain,
        gap,
        status,
      ].join(','),
    )
  }

  mkdirSync(dirname(OUT), { recursive: true })
  writeFileSync(OUT, `${rows.join('\n')}\n`)

  const byCls = {}
  let gaps = 0
  for (const r of rows.slice(1)) {
    const c = r.split(',')[5]
    byCls[c] = (byCls[c] ?? 0) + 1
    if (r.includes('NO_FRONTEND_CONSUMER') || r.includes('UNCLASSIFIED')) gaps += 1
  }
  console.log(JSON.stringify({ out: OUT, total: routes.length, byClassification: byCls, gaps }, null, 2))
}

main()
