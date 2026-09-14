import { api } from './client'

/**
 * 原始日志与自动扩缩容事实（设计规范 §7.2 / §6.3）
 *
 * 说明：`/metrics/query_range` 已按设计关闭任意 PromQL 直通
 * （返回 METRICS_PROMQL_RANGE_DISABLED），因此不再作为前端能力消费。
 */

export interface RawLogRow {
  /** LogsQL 返回的记录；字段随来源变化，只做展示不推断语义 */
  fields: Record<string, unknown>
  timestamp?: string
  message?: string
  level?: string
  [key: string]: unknown
}

function parseJSONL(body: unknown): Record<string, unknown>[] {
  if (Array.isArray(body)) return body as Record<string, unknown>[]
  if (typeof body !== 'string') {
    if (body && typeof body === 'object') {
      const obj = body as Record<string, unknown>
      if (Array.isArray(obj.logs)) return obj.logs as Record<string, unknown>[]
      if (Array.isArray(obj.data)) return obj.data as Record<string, unknown>[]
    }
    return []
  }
  const rows: Record<string, unknown>[] = []
  for (const line of body.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    try {
      const parsed = JSON.parse(trimmed)
      if (parsed && typeof parsed === 'object') rows.push(parsed as Record<string, unknown>)
    } catch {
      // LogsQL 可能返回非 JSON 行（错误文本）；不作为结构化事实使用。
    }
  }
  return rows
}

function toRow(raw: Record<string, unknown>): RawLogRow {
  const ts = (raw._time ?? raw.time ?? raw.timestamp ?? raw.ts) as string | undefined
  const message = (raw._msg ?? raw.message ?? raw.body ?? raw.log) as string | undefined
  const level = (raw.level ?? raw.severity) as string | undefined
  return {
    ...raw,
    fields: raw,
    ...(typeof ts === 'string' ? { timestamp: ts } : {}),
    ...(typeof message === 'string' ? { message } : {}),
    ...(typeof level === 'string' ? { level } : {}),
  }
}

/**
 * 读取原始日志。query 为服务端可接受的 LogsQL 片段；前端不构造任意查询语言，
 * 只传受限的 scope 标签与时间范围。
 */
export async function queryRawLogs(
  params: { serviceName?: string; minutes?: number; limit?: number },
  signal?: AbortSignal,
): Promise<RawLogRow[]> {
  const minutes = params.minutes ?? 60
  const limit = Math.min(Math.max(params.limit ?? 50, 1), 200)
  // 只传时间窗与业务过滤条件：tenant/cluster 由服务端按已验证会话强制注入，
  // 前端自报 scope 既无效也不被信任。
  const parts = [`_time:${minutes}m`]
  if (params.serviceName) parts.push(`service_name:"${params.serviceName}"`)
  const res = await api.get('/logs/victorialogs', { params: { query: parts.join(' '), limit }, signal })
  return parseJSONL(res.data).map(toRow)
}

// ===== 类型化指标与原生拓扑节点事实 =====
// 任意 PromQL 直通已按设计关闭；只使用带 service 的类型化指标契约。

export interface TypedMetricPoint {
  service: string
  values: Record<string, unknown>[]
}

export async function queryServiceMetrics(
  params: { clusterId: string; service: string },
  signal?: AbortSignal,
): Promise<TypedMetricPoint> {
  const res = await api.get<{ count?: number; data?: Record<string, unknown>[]; service?: string }>('/metrics/query', {
    params: { cluster_id: params.clusterId, service: params.service },
    signal,
  })
  return { service: res.data?.service ?? params.service, values: res.data?.data ?? [] }
}

export interface TopologyNodeFact {
  name: string
  latencyMs: number | null
  errorRate: number | null
  throughput: number | null
  healthScore: number | null
  apdex: number | null
  calls: number | null
  errors: number | null
}

export async function getTopologyNode(
  params: { clusterId: string; name: string },
  signal?: AbortSignal,
): Promise<TopologyNodeFact> {
  const res = await api.get<Record<string, unknown>>(`/topology/node/${encodeURIComponent(params.name)}`, {
    params: { cluster_id: params.clusterId },
    signal,
  })
  const d = res.data ?? {}
  const metrics = (d.metrics ?? {}) as Record<string, unknown>
  const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)
  return {
    name: String(d.name ?? params.name),
    latencyMs: num(d.latency_ms),
    errorRate: num(d.error_rate),
    throughput: num(d.throughput),
    healthScore: num(d.health_score),
    apdex: num(d.apdex),
    calls: num(metrics.calls),
    errors: num(metrics.errors),
  }
}

export interface HpaFact {
  namespace: string
  name: string
  minReplicas: number | null
  maxReplicas: number | null
  currentReplicas: number | null
  desiredReplicas: number | null
  utilization: number | null
  metricName: string
}

export async function listHpa(signal?: AbortSignal): Promise<{ items: HpaFact[]; error?: string }> {
  const res = await api.get<{ hpa?: Record<string, unknown>[]; error?: string }>('/infrastructure/hpa', { signal })
  const raw = res.data?.hpa ?? []
  const items: HpaFact[] = raw.map((r) => ({
    namespace: String(r.namespace ?? ''),
    name: String(r.name ?? ''),
    minReplicas: typeof r.min_replicas === 'number' ? r.min_replicas : null,
    maxReplicas: typeof r.max_replicas === 'number' ? r.max_replicas : null,
    currentReplicas: typeof r.current_replicas === 'number' ? r.current_replicas : null,
    desiredReplicas: typeof r.desired_replicas === 'number' ? r.desired_replicas : null,
    utilization: typeof r.utilization === 'number' ? r.utilization : null,
    metricName: String(r.metric_name ?? ''),
  }))
  return { items, ...(res.data?.error ? { error: res.data.error } : {}) }
}
