import { describe, expect, it } from 'vitest'
import type { GraphEntity } from '../../api/graphContracts'
import {
  isSelectableResourceType,
  resourceDomainOf,
  resourceLocation,
  resourceTypeLabel,
  toPlatformResourceRef,
} from './resourceDomain'

function entity(entity_type: string, overrides: Partial<GraphEntity> = {}): GraphEntity {
  return {
    entity_uid: `uid:${entity_type}`,
    entity_type,
    tenant_id: 'tenant-a',
    cluster_id: 'cluster-a',
    namespace: entity_type === 'pod' ? 'payments' : undefined,
    name: 'node-01',
    name_key: 'node-01',
    source: 'graph',
    status: 'active',
    confidence: 1,
    generation: 1,
    attrs_version: 1,
    ...overrides,
  }
}

describe('resource domain mapping', () => {
  it('keeps physical server, Kubernetes node, and VM distinct within compute', () => {
    expect(resourceDomainOf('physical_server')).toBe('compute')
    expect(resourceDomainOf('k8s_node')).toBe('compute')
    expect(resourceDomainOf('vm')).toBe('compute')
    expect(resourceTypeLabel('physical_server')).toBe('物理服务器')
    expect(resourceTypeLabel('k8s_node')).toBe('Kubernetes 节点')
    expect(resourceTypeLabel('vm')).toBe('虚拟机')
  })

  it('maps only platform resource types to a selectable domain', () => {
    expect(resourceDomainOf('namespace')).toBe('kubernetes')
    expect(resourceDomainOf('network')).toBe('network')
    expect(resourceDomainOf('pvc')).toBe('storage')
    expect(resourceDomainOf('service')).toBe('application')
    for (const type of ['cpu', 'dimm', 'alert', 'change', 'case', 'sel_event', 'migration', 'unknown']) {
      expect(isSelectableResourceType(type), type).toBe(false)
      expect(resourceDomainOf(type), type).toBeNull()
    }
  })

  it('rejects a resource from a different active cluster', () => {
    expect(() => toPlatformResourceRef(entity('pod'), 'cluster-b')).toThrow('跨集群资源')
  })

  it('projects Kubernetes namespace only for Kubernetes resources', () => {
    expect(toPlatformResourceRef(entity('pod'), 'cluster-a')).toMatchObject({
      clusterId: 'cluster-a', uid: 'uid:pod', type: 'pod', domain: 'kubernetes', name: 'node-01', namespace: 'payments',
    })
    expect(toPlatformResourceRef(entity('physical_server', { namespace: 'should-not-appear' }), 'cluster-a')).toMatchObject({
      type: 'physical_server', domain: 'compute', name: 'node-01',
    })
    expect(toPlatformResourceRef(entity('physical_server', { namespace: 'should-not-appear' }), 'cluster-a')).not.toHaveProperty('namespace')
  })

  it('formats a resource location without inventing namespace or node fields', () => {
    expect(resourceLocation({ clusterId: 'cluster-a', uid: 'uid:pod', type: 'pod', domain: 'kubernetes', name: 'api', namespace: 'payments' })).toBe('payments / api')
    expect(resourceLocation({ clusterId: 'cluster-a', uid: 'uid:server', type: 'physical_server', domain: 'compute', name: 'server-01' })).toBe('server-01')
  })
})
