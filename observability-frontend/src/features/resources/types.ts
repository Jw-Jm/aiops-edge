import type { GraphEntityType } from '../../api/graphContracts'

export type ResourceDomain = 'compute' | 'network' | 'storage' | 'kubernetes' | 'application'

export interface PlatformResourceRef {
  clusterId: string
  uid: string
  type: GraphEntityType
  domain: ResourceDomain
  name: string
  namespace?: string
}

export type ResourceHealth = 'critical' | 'degraded' | 'risk' | 'healthy' | 'unknown'

export const RESOURCE_DOMAINS: readonly ResourceDomain[] = ['compute', 'network', 'storage', 'kubernetes', 'application'] as const
