import { api } from './client'
import type { ResourceReadMeta, ResourceReadMetaView } from './resources'

export type PlatformClusterHealth = 'healthy' | 'degraded' | 'critical' | 'unknown'

export interface PlatformCoverage {
  covered: number
  expected: number
  ratio: number | null
}

export interface PlatformIssue {
  clusterId: string
  resourceUid: string
  ruleId: string
  severity: string
  status: string
  title: string
  observedAt?: string
}

export interface PlatformCapabilitySummary {
  healthy: number
  total: number
  issues: string[]
}

export interface PlatformOverview {
  activeClusterId?: string
  activeCriticalIssues: number
  managedClusters: number
  affectedClusters: number
  unknownOrStaleClusters: number
  clusterStates: Record<PlatformClusterHealth, number>
  coverage: PlatformCoverage
  freshestAt?: string
  oldestValidAt?: string
  highestPriority?: PlatformIssue
  capabilitySummary: PlatformCapabilitySummary
  meta: ResourceReadMetaView
}

export interface PlatformCluster {
  clusterId: string
  name: string
  /** 观测健康：healthy | degraded | critical | unknown */
  status: PlatformClusterHealth
  /** 接入/注册状态（active/ready/…）：只描述生命周期，不产生健康结论 */
  registrationStatus: string
  /** 状态原因，供用户下钻判断依据 */
  statusReason: string
  stale: boolean
  covered: boolean
  updatedAt?: string
}

export interface PlatformClustersResponse {
  clusters: PlatformCluster[]
  count: number
  total: number
  meta: ResourceReadMetaView
}

interface PlatformIssueWire {
  cluster_id: string
  resource_uid: string
  rule_id: string
  severity: string
  status: string
  title: string
  observed_at?: string
}

interface PlatformOverviewWire {
  active_cluster_id?: string
  active_critical_issues: number
  managed_clusters: number
  affected_clusters: number
  unknown_or_stale_clusters: number
  cluster_states: Partial<Record<PlatformClusterHealth, number>>
  coverage: { covered: number; expected: number; ratio?: number }
  freshest_at?: string
  oldest_valid_at?: string
  highest_priority?: PlatformIssueWire
  capability_summary?: { healthy?: number; total?: number; issues?: string[] }
  meta: ResourceReadMeta
}

interface PlatformClusterWire {
  cluster_id: string
  name: string
  status: PlatformClusterHealth
  registration_status?: string
  status_reason?: string
  stale?: boolean
  covered?: boolean
  updated_at?: string
}

interface PlatformClustersWire {
  clusters: PlatformClusterWire[]
  count: number
  total: number
  meta: ResourceReadMeta
}

function mapMeta(meta: ResourceReadMeta): ResourceReadMetaView {
  return {
    generatedAt: meta.generated_at,
    partial: meta.partial,
    stale: meta.stale,
    warningCodes: meta.warning_codes ?? [],
  }
}

function mapIssue(issue: PlatformIssueWire): PlatformIssue {
  return {
    clusterId: issue.cluster_id,
    resourceUid: issue.resource_uid,
    ruleId: issue.rule_id,
    severity: issue.severity,
    status: issue.status,
    title: issue.title,
    ...(issue.observed_at ? { observedAt: issue.observed_at } : {}),
  }
}

function mapClusterStates(states: Partial<Record<PlatformClusterHealth, number>>): Record<PlatformClusterHealth, number> {
  return {
    healthy: states.healthy ?? 0,
    degraded: states.degraded ?? 0,
    critical: states.critical ?? 0,
    unknown: states.unknown ?? 0,
  }
}

export function mapPlatformOverview(wire: PlatformOverviewWire): PlatformOverview {
  return {
    ...(wire.active_cluster_id ? { activeClusterId: wire.active_cluster_id } : {}),
    activeCriticalIssues: wire.active_critical_issues,
    managedClusters: wire.managed_clusters,
    affectedClusters: wire.affected_clusters,
    unknownOrStaleClusters: wire.unknown_or_stale_clusters,
    clusterStates: mapClusterStates(wire.cluster_states),
    coverage: { covered: wire.coverage.covered, expected: wire.coverage.expected, ratio: wire.coverage.ratio ?? null },
    ...(wire.freshest_at ? { freshestAt: wire.freshest_at } : {}),
    ...(wire.oldest_valid_at ? { oldestValidAt: wire.oldest_valid_at } : {}),
    ...(wire.highest_priority ? { highestPriority: mapIssue(wire.highest_priority) } : {}),
    capabilitySummary: {
      healthy: wire.capability_summary?.healthy ?? 0,
      total: wire.capability_summary?.total ?? 0,
      issues: wire.capability_summary?.issues ?? [],
    },
    meta: mapMeta(wire.meta),
  }
}

export async function getPlatformOverview(signal?: AbortSignal): Promise<PlatformOverview> {
  const response = await api.get<PlatformOverviewWire>('/platform/overview', { signal })
  return mapPlatformOverview(response.data)
}

export async function getPlatformClusters(params: { status?: PlatformClusterHealth; q?: string; limit?: number; cursor?: string } = {}, signal?: AbortSignal): Promise<PlatformClustersResponse> {
  const response = await api.get<PlatformClustersWire>('/platform/clusters', { params, signal })
  return {
    clusters: response.data.clusters.map((item) => ({
      clusterId: item.cluster_id,
      name: item.name,
      status: item.status,
      registrationStatus: item.registration_status ?? '',
      statusReason: item.status_reason ?? '',
      stale: item.stale === true,
      covered: item.covered !== false,
      ...(item.updated_at ? { updatedAt: item.updated_at } : {}),
    })),
    count: response.data.count,
    total: response.data.total,
    meta: mapMeta(response.data.meta),
  }
}

// ===== 集群口径容量事实（设计规范 §6.2 / §6.3）=====
// CPU = 已使用核数 ÷ 可分配核数；内存 = 已使用容量 ÷ 可分配容量。
// 必须同时给出 P95 与最大值、热点节点数，避免集群平均掩盖单节点热点。

export interface CapacityResourceFact {
  used: number
  allocatable: number
  usageRatio: number | null
  unit: string
  aggregation: string
  source: string
  sourceTimestamp: string
}

export interface CapacityClusterFact {
  clusterId: string
  name: string
  cpu: CapacityResourceFact
  memory: CapacityResourceFact
  nodes: { total: number; ready: number; notReady: number; unknown: number }
  p95CpuUtilization: number | null
  p95MemUtilization: number | null
  maxCpuUtilization: number | null
  maxMemUtilization: number | null
  hotNodeCount: number
  hotNodeThresholdPct: number
  quality: string
  qualityReason?: string
  registrationStatus: string
}

interface CapacityWire {
  cluster_id: string
  name: string
  cpu: { used: number; allocatable: number; usage_ratio: number | null; unit: string; aggregation: string; source: string; source_timestamp: string }
  memory: { used: number; allocatable: number; usage_ratio: number | null; unit: string; aggregation: string; source: string; source_timestamp: string }
  nodes: { total: number; ready: number; not_ready: number; unknown: number }
  p95_cpu_utilization: number | null
  p95_mem_utilization: number | null
  max_cpu_utilization: number | null
  max_mem_utilization: number | null
  hot_node_count: number
  hot_node_threshold_pct: number
  quality: string
  quality_reason?: string
  registration_status: string
}

export interface PlatformCapacityResponse {
  clusters: CapacityClusterFact[]
  count: number
  meta: ResourceReadMetaView
}

export async function getPlatformCapacity(signal?: AbortSignal): Promise<PlatformCapacityResponse> {
  const response = await api.get<{ generated_at: string; clusters: CapacityWire[]; count: number; meta: ResourceReadMeta }>(
    '/platform/capacity',
    { signal },
  )
  return {
    clusters: (response.data.clusters ?? []).map((c) => ({
      clusterId: c.cluster_id,
      name: c.name,
      cpu: {
        used: c.cpu.used,
        allocatable: c.cpu.allocatable,
        usageRatio: c.cpu.usage_ratio ?? null,
        unit: c.cpu.unit,
        aggregation: c.cpu.aggregation,
        source: c.cpu.source,
        sourceTimestamp: c.cpu.source_timestamp,
      },
      memory: {
        used: c.memory.used,
        allocatable: c.memory.allocatable,
        usageRatio: c.memory.usage_ratio ?? null,
        unit: c.memory.unit,
        aggregation: c.memory.aggregation,
        source: c.memory.source,
        sourceTimestamp: c.memory.source_timestamp,
      },
      nodes: { total: c.nodes.total, ready: c.nodes.ready, notReady: c.nodes.not_ready, unknown: c.nodes.unknown },
      p95CpuUtilization: c.p95_cpu_utilization ?? null,
      p95MemUtilization: c.p95_mem_utilization ?? null,
      maxCpuUtilization: c.max_cpu_utilization ?? null,
      maxMemUtilization: c.max_mem_utilization ?? null,
      hotNodeCount: c.hot_node_count,
      hotNodeThresholdPct: c.hot_node_threshold_pct,
      quality: c.quality,
      ...(c.quality_reason ? { qualityReason: c.quality_reason } : {}),
      registrationStatus: c.registration_status,
    })),
    count: response.data.count,
    meta: mapMeta(response.data.meta),
  }
}
