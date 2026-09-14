/**
 * 全链路监控路径选择 view model（设计规范 §6.4 / §7.3）
 *
 * 硬约束：
 *  - 直接进入页面时，推荐 selectedPathId 必须来自可审计的确定性风险排序；
 *    排序输入必须全部是服务端返回的真实事实，不得由前端编造。
 *  - 从其他页面进入时优先继承路径上下文（selectedPathId 查询参数）。
 *  - 端到端依赖、异常路径优先级、所选路径趋势、性能矩阵、证据列表
 *    必须绑定同一个 selectedPathId。
 */

export type DataQuality = 'healthy' | 'partial' | 'stale' | 'unknown' | 'not_connected' | 'failed'

export interface ObservabilityPath {
  pathId: string
  name: string
  /** 路径类型：控制面调用 / 虚机启动 / Pod 网络 / 卷挂载 / 镜像拉取 / 负载均衡 / DNS / Kubernetes 控制路径 */
  category: string
  status: string
  severity: number
  affectedResources: number
  /** 相对基线的偏离程度 0-1；服务端计算 */
  deviation: number
  /** 异常持续时间（秒） */
  durationSeconds: number
  quality: DataQuality
  sourceTimestamp?: string | null
}

export interface PathSelection {
  selectedPathId: string | null
  reason: string
  /** 参与排序的字段与权重，用于页面向用户解释"为什么推荐它" */
  factors: { label: string; value: string; weight: number }[]
}

const WEIGHTS = { severity: 0.35, affected: 0.25, deviation: 0.2, duration: 0.1, quality: 0.1 } as const

const QUALITY_PENALTY: Record<DataQuality, number> = {
  healthy: 1,
  partial: 0.6,
  stale: 0.5,
  unknown: 0.3,
  not_connected: 0.2,
  failed: 0.1,
}

export function riskScore(path: ObservabilityPath): number {
  const severity = clamp01(path.severity / 4)
  const affected = clamp01(path.affectedResources / 20)
  const deviation = clamp01(path.deviation)
  const duration = clamp01(path.durationSeconds / 3600)
  const quality = QUALITY_PENALTY[path.quality] ?? 0.3
  const raw =
    severity * WEIGHTS.severity +
    affected * WEIGHTS.affected +
    deviation * WEIGHTS.deviation +
    duration * WEIGHTS.duration +
    quality * WEIGHTS.quality
  return Number(clamp01(raw).toFixed(4))
}

function clamp01(v: number): number {
  if (!Number.isFinite(v)) return 0
  return Math.min(1, Math.max(0, v))
}

/**
 * 确定性选择 recommended path。
 * inheritedPathId 存在且真实存在于目录中时优先继承（跨页面上下文继承）。
 * 完全相同分数时按 pathId 字典序，保证可复现。
 */
export function selectPath(paths: ObservabilityPath[], inheritedPathId?: string | null): PathSelection {
  if (paths.length === 0) {
    return { selectedPathId: null, reason: '路径目录为空或数据源未接入，无法选择路径', factors: [] }
  }
  const inherited = inheritedPathId ? paths.find((p) => p.pathId === inheritedPathId) : undefined
  if (inherited) {
    return {
      selectedPathId: inherited.pathId,
      reason: `继承来源页面的路径上下文（${inherited.name}）`,
      factors: factorsOf(inherited),
    }
  }
  const ranked = [...paths].sort((a, b) => riskScore(b) - riskScore(a) || a.pathId.localeCompare(b.pathId))
  const best = ranked[0]
  return {
    selectedPathId: best.pathId,
    reason: `确定性风险排序最高：严重度 ${best.severity}、影响对象 ${best.affectedResources}、偏离基线 ${(best.deviation * 100).toFixed(0)}%、持续 ${Math.round(best.durationSeconds / 60)} 分钟、数据质量 ${qualityLabel(best.quality)}`,
    factors: factorsOf(best),
  }
}

function factorsOf(path: ObservabilityPath): PathSelection['factors'] {
  return [
    { label: '严重度', value: String(path.severity), weight: WEIGHTS.severity },
    { label: '影响范围', value: `${path.affectedResources} 个对象`, weight: WEIGHTS.affected },
    { label: '偏离基线', value: `${(path.deviation * 100).toFixed(0)}%`, weight: WEIGHTS.deviation },
    { label: '持续时间', value: `${Math.round(path.durationSeconds / 60)} 分钟`, weight: WEIGHTS.duration },
    { label: '数据质量', value: qualityLabel(path.quality), weight: WEIGHTS.quality },
  ]
}

const QUALITY_LABELS: Record<DataQuality, string> = {
  healthy: '数据完整',
  partial: '部分数据',
  stale: '数据陈旧',
  unknown: '质量未知',
  not_connected: '未接入',
  failed: '采集失败',
}

export function qualityLabel(q: DataQuality): string {
  return QUALITY_LABELS[q] ?? '质量未知'
}

/** 云平台路径类型白名单：禁止业务示例（下单、商品查询） */
export const ALLOWED_PATH_CATEGORIES = [
  '控制面调用',
  '虚拟机启动',
  'Pod 网络',
  '卷挂载',
  '镜像拉取',
  '负载均衡',
  'DNS',
  'Kubernetes 控制路径',
  '计算依赖路径',
  '网络依赖路径',
  '存储依赖路径',
] as const
