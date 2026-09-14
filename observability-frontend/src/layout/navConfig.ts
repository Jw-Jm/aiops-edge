import type { AppIconName } from '../components/AppIcons'

/**
 * V1.4 八页最终信息架构（唯一验收基线 §4.1）
 *
 * 顺序固定且不可调整：
 *   AI 智能运维 → 总览 → 集群 → 全链路监控 → 知识图谱 → 知识库 → 报告 → 设置
 *
 * 禁止：问题 / 资源 / 调查 / 处置 独立入口；禁止按角色隐藏完整导航（D-01）。
 * 告警、资源详情、调查阶段、处置记录全部作为上述页面内的上下文对象（B 类）。
 */

export type PrimaryPageId =
  | 'ai-operations'
  | 'overview'
  | 'clusters'
  | 'observability'
  | 'knowledge-graph'
  | 'knowledge'
  | 'reports'
  | 'settings'

export interface NavItem {
  id: PrimaryPageId
  /** 规范路由；集群页使用 :clusterUid 占位，由 clusterPath() 实例化 */
  path: string
  label: string
  icon: AppIconName
  /** 该页面是否必须绑定一个服务端授权集群 */
  requiresCluster: boolean
  /** 用于高亮判定：路径前缀 */
  matchPrefixes: string[]
}

export const AI_OPERATIONS_PATH = '/ai-operations'

export const PRIMARY_NAV: readonly NavItem[] = [
  {
    id: 'ai-operations',
    path: '/ai-operations',
    label: 'AI 智能运维',
    icon: 'sparkles',
    requiresCluster: false,
    matchPrefixes: ['/ai-operations'],
  },
  {
    id: 'overview',
    path: '/overview',
    label: '总览',
    icon: 'overview',
    requiresCluster: false,
    matchPrefixes: ['/overview'],
  },
  {
    id: 'clusters',
    path: '/clusters/:clusterUid/overview',
    label: '集群',
    icon: 'cluster',
    requiresCluster: true,
    matchPrefixes: ['/clusters'],
  },
  {
    id: 'observability',
    path: '/observability',
    label: '全链路监控',
    icon: 'traces',
    requiresCluster: false,
    matchPrefixes: ['/observability'],
  },
  {
    id: 'knowledge-graph',
    path: '/knowledge-graph',
    label: '知识图谱',
    icon: 'topology',
    requiresCluster: false,
    matchPrefixes: ['/knowledge-graph'],
  },
  {
    id: 'knowledge',
    path: '/knowledge',
    label: '知识库',
    icon: 'knowledge',
    requiresCluster: false,
    matchPrefixes: ['/knowledge', '/knowledge-graph'],
  },
  {
    id: 'reports',
    path: '/reports',
    label: '报告',
    icon: 'reports',
    requiresCluster: false,
    matchPrefixes: ['/reports'],
  },
  {
    id: 'settings',
    path: '/settings',
    label: '设置',
    icon: 'settings',
    requiresCluster: false,
    matchPrefixes: ['/settings'],
  },
] as const

/** 设置页分区（§4.4） */
export const SETTINGS_SECTIONS = [
  { id: 'overview', label: '状态总览' },
  { id: 'integrations', label: '数据接入与目录' },
  { id: 'graph', label: '图谱同步' },
  { id: 'llm', label: 'LLM 与路由' },
  { id: 'workflow', label: 'Agent 与 Workflow' },
  { id: 'mcp', label: 'MCP 与工具' },
  { id: 'rag', label: '知识与 RAG' },
  { id: 'security', label: '策略与安全' },
  { id: 'health', label: '平台自身健康' },
  { id: 'evaluation', label: '评测与运行' },
] as const

export type SettingsSectionId = (typeof SETTINGS_SECTIONS)[number]['id']

/**
 * 统一账户：所有一级入口对任意已认证账户可见。
 * 保留该函数签名以便调用方逐步迁移，但不再按角色过滤（D-01）。
 */
export function visiblePrimaryNav(_role?: string): readonly NavItem[] {
  return PRIMARY_NAV
}

export type ClusterWorkspace = 'overview'

/** 集群页规范路由：/clusters/:clusterUid/overview */
export function clusterPath(clusterId: string, workspace: ClusterWorkspace = 'overview'): string {
  const encoded = encodeURIComponent(clusterId)
  return `/clusters/${encoded}/${workspace}`
}

/** 由 URL 解析出集群工作区路径参数 */
export function parseClusterRoute(pathname: string): { clusterUid: string; section: string | null } | null {
  const m = /^\/clusters\/([^/]+)(?:\/([^/]+))?/.exec(pathname)
  if (!m) return null
  return { clusterUid: decodeURIComponent(m[1]), section: m[2] ?? null }
}

export function isPrimaryNavActive(pathname: string, item: NavItem): boolean {
  return item.matchPrefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`))
}

/** 稳定查询参数：刷新后必须可复现（§4.3） */
export const STABLE_QUERY_KEYS = [
  'scope',
  'tenant',
  'cluster',
  'clusterUid',
  'time',
  'from',
  'to',
  'tab',
  'section',
  'selection',
  'resourceUid',
  'selectedPathId',
  'selectedRootEntityId',
  'selectedRelationId',
  'generation',
  'taskId',
  'runId',
  'reportId',
  'reportRunId',
  'knowledgeId',
  'version',
  'sourcePage',
  'signal',
] as const

/**
 * 旧路由迁移表（附录 A）。
 *
 * 三类目标：
 *   - 静态目标：直接 replace
 *   - 集群目标：需要 canonical clusterUid（来自 URL 或当前 scope），保留可转换参数
 *   - 无法转换：重定向到安全默认视图并标记 migrated=0 供页面显示迁移说明
 */
interface LegacyRule {
  pattern: RegExp
  /** 返回目标；（clusterUid, search）→ 目标串 */
  target: (clusterUid: string | null, legacyPath: string) => string
  /** 是否可无损转换 */
  convertible: (clusterUid: string | null) => boolean
}

const legacyRules: LegacyRule[] = [
  { pattern: /^\/$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/ai\/chat$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/assistant$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/investigation(\/.*)?$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/actions$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/admin\/approvals$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/ops(\/.*)?$/, target: () => AI_OPERATIONS_PATH, convertible: () => true },
  { pattern: /^\/graph$/, target: () => '/knowledge-graph', convertible: () => true },
  { pattern: /^\/observability\/relationships$/, target: () => '/knowledge-graph', convertible: () => true },
  { pattern: /^\/kg(\/.*)?$/, target: () => '/knowledge-graph', convertible: () => true },
  { pattern: /^\/knowledge(\/.*)?$/, target: () => '/knowledge', convertible: () => true },
  { pattern: /^\/report$/, target: () => '/reports', convertible: () => true },
  { pattern: /^\/dashboards(\/.*)?$/, target: () => '/reports', convertible: () => true },
  { pattern: /^\/observe$/, target: () => '/observability', convertible: () => true },
  { pattern: /^\/observability\/(service|trace|log|grafana|metrics|traces|logs|events|changes)$/, target: () => '/observability', convertible: () => true },
  { pattern: /^\/telemetry(\/.*)?$/, target: () => '/observability', convertible: () => true },
  { pattern: /^\/changes$/, target: () => '/observability', convertible: () => true },
  { pattern: /^\/alerts\/events(\/.*)?$/, target: () => '/overview', convertible: () => true },
  { pattern: /^\/problems(\/.*)?$/, target: () => '/overview', convertible: () => true },
  { pattern: /^\/alerts\/rules(\/.*)?$/, target: () => '/settings?section=integrations', convertible: () => true },
  { pattern: /^\/alerts\/silences(\/.*)?$/, target: () => '/settings?section=integrations', convertible: () => true },
  { pattern: /^\/admin$/, target: () => '/settings', convertible: () => true },
  { pattern: /^\/admin\/settings(\/.*)?$/, target: () => '/settings', convertible: () => true },
  { pattern: /^\/admin\/users(\/.*)?$/, target: () => '/settings?section=security', convertible: () => true },
  { pattern: /^\/admin\/graph-operations(\/.*)?$/, target: () => '/settings?section=graph', convertible: () => true },
  { pattern: /^\/system(\/.*)?$/, target: () => '/settings', convertible: () => true },
  { pattern: /^\/capabilities(\/.*)?$/, target: () => '/settings', convertible: () => true },
  { pattern: /^\/security(\/.*)?$/, target: () => '/settings?section=security', convertible: () => true },
  { pattern: /^\/credentials(\/.*)?$/, target: () => '/settings?section=security', convertible: () => true },
  { pattern: /^\/components(\/.*)?$/, target: () => '/settings?section=health', convertible: () => true },
  { pattern: /^\/cluster$/, target: (c) => (c ? clusterPath(c) : '/clusters'), convertible: (c) => Boolean(c) },
  // 需要 canonical clusterUid 的旧入口
  {
    pattern: /^\/clusters$/,
    target: (c) => (c ? clusterPath(c) : '/clusters'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/clusters\/([^/]+)\/resources(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?tab=resources` : '/clusters'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/clusters\/([^/]+)\/(graph)$/,
    target: () => '/knowledge-graph',
    convertible: () => true,
  },
  {
    pattern: /^\/clusters\/([^/]+)\/(assistant|investigations|actions|approvals)(\/.*)?$/,
    target: () => AI_OPERATIONS_PATH,
    convertible: () => true,
  },
  {
    pattern: /^\/clusters\/([^/]+)\/knowledge(\/.*)?$/,
    target: () => '/knowledge',
    convertible: () => true,
  },
  {
    pattern: /^\/clusters\/([^/]+)\/reports(\/.*)?$/,
    target: () => '/reports',
    convertible: () => true,
  },
  {
    pattern: /^\/clusters\/([^/]+)\/(observe)$/,
    target: () => '/observability',
    convertible: () => true,
  },
  {
    pattern: /^\/clusters\/([^/]+)$/,
    target: (c) => (c ? clusterPath(c) : '/clusters'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/resources(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?tab=resources` : '/knowledge-graph'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/capacity(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?section=capacity` : '/overview'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/infra\/k8s(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?tab=resources&domain=kubernetes` : '/overview'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/hardware(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?section=compute` : '/overview'),
    convertible: (c) => Boolean(c),
  },
  {
    pattern: /^\/observability\/vms(\/.*)?$/,
    target: (c) => (c ? `${clusterPath(c)}?section=compute&type=vm` : '/observability'),
    convertible: (c) => Boolean(c),
  },
]

export interface MigrationResult {
  /** 目标 URL（含保留参数） */
  target: string
  /** true=无损转换；false=已做有损转换，页面需显示迁移说明 */
  convertible: boolean
  /** 原路径，供迁移说明展示 */
  from: string
}

/**
 * 解析旧路由迁移目标。
 * 返回 null 表示当前路径不是已知旧路由（调用方应回落到 404 或安全默认视图）。
 */
export function resolveLegacyRoute(
  pathname: string,
  search: string,
  activeClusterUid?: string | null,
): MigrationResult | null {
  for (const rule of legacyRules) {
    if (!rule.pattern.test(pathname)) continue
    const m = rule.pattern.exec(pathname)
    const urlClusterUid = m?.[1] && /^[0-9a-fA-F-]{36}$|^[^/]+$/.test(m[1]) ? m[1] : null
    const clusterUid = urlClusterUid ?? activeClusterUid ?? null
    const [targetPath, targetSearch = ''] = rule.target(clusterUid, pathname).split('?')
    const params = new URLSearchParams(targetSearch)
    // 保留可转换参数（§4.3 + 附录 A）：稳定键与对象/信号过滤参数一并透传，
    // 无法映射的参数由 converter 决定是否丢弃，绝不静默跳到无关默认页。
    const incoming = new URLSearchParams(search)
    for (const [key, value] of incoming.entries()) {
      if (!params.has(key)) params.set(key, value)
    }
    void STABLE_QUERY_KEYS
    const convertible = rule.convertible(clusterUid)
    if (!convertible) params.set('migration', 'partial')
    params.set('from', pathname)
    const qs = params.toString()
    return { target: `${targetPath}${qs ? `?${qs}` : ''}`, convertible, from: pathname }
  }
  return null
}

/** 兼容旧调用点：仅静态部分 */
export const LEGACY_REDIRECTS: ReadonlyMap<string, string> = new Map(
  legacyRules
    .filter((rule) => rule.pattern.source.startsWith('^\\/') && !rule.pattern.source.includes('/clusters/'))
    .map((rule) => {
      const sample = rule.pattern.source.replace(/^\^/, '').replace(/\$$/, '').replace(/\\\//g, '/').replace(/\/(.*)\?\)\?$/,'')
      return [sample, rule.target(null, sample)] as [string, string]
    }),
)

export function legacyTarget(pathname: string): string | null {
  const result = resolveLegacyRoute(pathname, '')
  return result ? result.target : null
}
