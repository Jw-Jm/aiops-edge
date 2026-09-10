import { api } from './client'
import type { PlatformClusterHealth, PlatformCoverage, PlatformIssue } from './platform'
import type { ResourceReadMeta, ResourceReadMetaView } from './resources'

export type ResourceKind = 'deployment' | 'statefulset' | 'daemonset' | 'job' | 'cronjob' | 'pod' | 'k8s_service' | 'ingress'
export type FoundationKind = 'control_plane' | 'nodes_hosts' | 'network' | 'storage' | 'kubevirt'

export interface ResourceKindSummary {
  kind: ResourceKind
  total: number
  abnormal: number
  unknownOrStale: number
}

export interface FoundationFact {
  kind: FoundationKind
  status: PlatformClusterHealth
  reason: string
  affectedResourceCount: number
}

export interface KubeVirtSummary {
  vm: number
  vmi: number
  notReady: number
  migrating: number
  failedMigration: number
  storageAffected: number
  networkAffected: number
}

export interface ClusterOverview {
  clusterId: string
  name: string
  status: PlatformClusterHealth
  statusReasons: string[]
  version?: string
  lastSyncAt?: string
  coverage: PlatformCoverage
  issues: PlatformIssue[]
  resourceKinds: ResourceKindSummary[]
  kubevirt: KubeVirtSummary
  foundation: FoundationFact[]
  meta: ResourceReadMetaView
}

interface ClusterOverviewWire {
  cluster_id: string
  name: string
  status: PlatformClusterHealth
  status_reasons?: string[]
  version?: string
  last_sync_at?: string
  coverage: { covered: number; expected: number; ratio?: number }
  issues?: Array<{ cluster_id: string; resource_uid: string; rule_id: string; severity: string; status: string; title: string; observed_at?: string }>
  resource_kinds?: Array<{ kind: ResourceKind; total: number; abnormal: number; unknown_or_stale: number }>
  kubevirt?: { vm?: number; vmi?: number; not_ready?: number; migrating?: number; failed_migration?: number; storage_affected?: number; network_affected?: number }
  foundation?: Array<{ kind: FoundationKind; status: PlatformClusterHealth; reason: string; affected_resource_count: number }>
  meta: ResourceReadMeta
}

const DEFAULT_KINDS: ResourceKind[] = ['deployment', 'statefulset', 'daemonset', 'job', 'cronjob', 'pod', 'k8s_service', 'ingress']

function mapMeta(meta: ResourceReadMeta): ResourceReadMetaView {
  return { generatedAt: meta.generated_at, partial: meta.partial, stale: meta.stale, warningCodes: meta.warning_codes ?? [] }
}

function mapIssue(item: NonNullable<ClusterOverviewWire['issues']>[number]): PlatformIssue {
  return { clusterId: item.cluster_id, resourceUid: item.resource_uid, ruleId: item.rule_id, severity: item.severity, status: item.status, title: item.title, ...(item.observed_at ? { observedAt: item.observed_at } : {}) }
}

export function mapClusterOverview(wire: ClusterOverviewWire): ClusterOverview {
  const byKind = new Map((wire.resource_kinds ?? []).map((item) => [item.kind, item]))
  return {
    clusterId: wire.cluster_id,
    name: wire.name,
    status: wire.status,
    statusReasons: wire.status_reasons ?? [],
    ...(wire.version ? { version: wire.version } : {}),
    ...(wire.last_sync_at ? { lastSyncAt: wire.last_sync_at } : {}),
    coverage: { covered: wire.coverage.covered, expected: wire.coverage.expected, ratio: wire.coverage.ratio ?? null },
    issues: (wire.issues ?? []).map(mapIssue),
    resourceKinds: DEFAULT_KINDS.map((kind) => {
      const item = byKind.get(kind)
      return { kind, total: item?.total ?? 0, abnormal: item?.abnormal ?? 0, unknownOrStale: item?.unknown_or_stale ?? 0 }
    }),
    kubevirt: {
      vm: wire.kubevirt?.vm ?? 0,
      vmi: wire.kubevirt?.vmi ?? 0,
      notReady: wire.kubevirt?.not_ready ?? 0,
      migrating: wire.kubevirt?.migrating ?? 0,
      failedMigration: wire.kubevirt?.failed_migration ?? 0,
      storageAffected: wire.kubevirt?.storage_affected ?? 0,
      networkAffected: wire.kubevirt?.network_affected ?? 0,
    },
    foundation: (wire.foundation ?? []).map((item) => ({ kind: item.kind, status: item.status, reason: item.reason, affectedResourceCount: item.affected_resource_count })),
    meta: mapMeta(wire.meta),
  }
}

export async function getClusterOverview(clusterId: string, signal?: AbortSignal): Promise<ClusterOverview> {
  const response = await api.get<ClusterOverviewWire>(`/clusters/${encodeURIComponent(clusterId)}/overview`, { signal })
  return mapClusterOverview(response.data)
}
