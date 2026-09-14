import { api } from './client'
import type { DataQuality } from '../pages/observability/pathSelection'

/**
 * 全链路监控查询合同（设计规范 §7.3）
 *
 * 服务端必须按 selectedPathId + scope + time 生成可复现关联视图；
 * 前端不得拼接不同路径的数据，也不得自行计算风险分。
 */

export interface ObservabilityPathWire {
  path_id: string
  name: string
  category: string
  status: string
  severity: number
  affected_resources: number
  deviation: number
  duration_seconds: number
  quality: string
  source_timestamp?: string | null
}

export interface ObservabilityPathCatalog {
  paths: ObservabilityPathWire[]
  count: number
  generated_at: string
  time_window: { from: string; to: string }
  /** 目录整体质量：任一来源 partial/stale 时不得声称完整 */
  quality: string
  quality_reason?: string
  partial: boolean
  stale: boolean
  sources: { source: string; state: string; last_success_at?: string | null; detail?: string }[]
}

export interface PathNodeWire {
  node_uid: string
  name: string
  node_type: string
  status: string
  latency_ms?: number | null
  error_rate?: number | null
  saturation?: number | null
  quality: string
}

export interface PathEdgeWire {
  edge_uid: string
  source_uid: string
  target_uid: string
  relation: string
  status: string
  latency_ms?: number | null
  error_rate?: number | null
}

export interface PathSegmentWire {
  segment: string
  latency_ms?: number | null
  error_rate?: number | null
  saturation?: number | null
  baseline_delta?: number | null
  sample_size?: number | null
  quality: string
}

export interface PathDetail {
  path_id: string
  name: string
  category: string
  nodes: PathNodeWire[]
  edges: PathEdgeWire[]
  segments: PathSegmentWire[]
  /** 端到端趋势，单位与聚合口径由服务端声明 */
  trend: {
    metric: string
    unit: string
    aggregation: string
    step_seconds: number
    points: { ts: string; latency_ms?: number | null; error_rate?: number | null; throughput?: number | null; saturation?: number | null }[]
  }
  events: {
    evidence_id: string
    type: string
    summary: string
    occurred_at: string
    resource_uid?: string | null
    quality: string
  }[]
  quality: string
  partial: boolean
  stale: boolean
  generated_at: string
  /** 证据缺口：服务端明确列出无法提供的部分 */
  gaps: string[]
}

function toQuality(value: unknown): DataQuality {
  const v = String(value ?? '').toLowerCase()
  if (v === 'healthy' || v === 'partial' || v === 'stale' || v === 'unknown' || v === 'not_connected' || v === 'failed') {
    return v
  }
  return 'unknown'
}

export function normalizeCatalog(wire: ObservabilityPathCatalog) {
  return {
    ...wire,
    paths: (wire.paths ?? []).map((p) => ({
      pathId: p.path_id,
      name: p.name,
      category: p.category,
      status: p.status,
      severity: p.severity,
      affectedResources: p.affected_resources,
      deviation: p.deviation,
      durationSeconds: p.duration_seconds,
      quality: toQuality(p.quality),
      sourceTimestamp: p.source_timestamp ?? null,
    })),
  }
}

export async function getObservabilityPaths(signal?: AbortSignal) {
  const res = await api.get<ObservabilityPathCatalog>('/observability/paths', { signal })
  return normalizeCatalog(res.data)
}

export async function getObservabilityPath(pathId: string, signal?: AbortSignal) {
  const res = await api.get<PathDetail>(`/observability/paths/${encodeURIComponent(pathId)}`, { signal })
  return res.data
}
