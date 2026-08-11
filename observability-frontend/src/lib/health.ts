// 健康度统一计算（全站一致口径，见设计方案 2.4）
// score = Apdex × 0.7 + (1 - 错误率) × 0.3
// 分档：>=0.9 健康 / 0.7~0.9 亚健康 / <0.7 异常
export type HealthLevel = 'healthy' | 'warning' | 'critical' | 'unknown'

export interface HealthResult {
  score: number | null
  level: HealthLevel
  label: string
}

export function computeHealth(opts: { apdex?: number | null; errorRate?: number | null }): HealthResult {
  const { apdex, errorRate } = opts
  // 两者都无数据 → 未知
  if ((apdex === undefined || apdex === null) && (errorRate === undefined || errorRate === null)) {
    return { score: null, level: 'unknown', label: '未知' }
  }
  const a = apdex ?? 0.5 // 缺失时取中性 0.5
  const e = errorRate ?? 0 // 缺失错误率视为 0
  const score = a * 0.7 + (1 - e) * 0.3
  const s = Math.max(0, Math.min(1, score))
  if (s >= 0.9) return { score: s, level: 'healthy', label: '健康' }
  if (s >= 0.7) return { score: s, level: 'warning', label: '亚健康' }
  return { score: s, level: 'critical', label: '异常' }
}

// 健康度 → 语义色（token 变量名）
export function healthColor(level: HealthLevel): string {
  switch (level) {
    case 'healthy': return 'var(--success)'
    case 'warning': return 'var(--warning)'
    case 'critical': return 'var(--danger)'
    default: return 'var(--text-muted)'
  }
}
