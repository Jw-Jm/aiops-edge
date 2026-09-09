import { describe, expect, it } from 'vitest'
import { projectResourceDetail } from './resourceDetailModel'
import type { ResourceDetailResponse } from '../../api/resources'

function detail(type: 'physical_server' | 'k8s_node' | 'vm'): ResourceDetailResponse {
  return {
    data: {
      clusterId: 'cluster-a', uid: `${type}:01`, type, domain: 'compute', name: 'edge-01', location: 'edge-01', health: 'healthy', source: 'inventory',
      attributes: type === 'physical_server'
        ? { vendor: 'Dell', model: 'R750', serial_number: 'SN-01', bmc_identifier: 'bmc-01', component_health: '全部正常' }
        : type === 'k8s_node'
          ? { role: 'worker', version: 'v1.31.0', ready: true, taints: [], capacity: '32 CPU / 128 GiB', host: 'edge-01' }
          : { namespace: 'payments', node: 'edge-01', cpu: '4', memory: '16Gi', disk: '100Gi', network: '10Gbps', migration: '无迁移' },
    },
    meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] },
  }
}

describe('resource detail projection', () => {
  it('keeps type-specific fields separate for physical server, Kubernetes node and VM', () => {
    expect(projectResourceDetail(detail('physical_server')).flatMap((section) => section.fields).map((field) => field.label)).toEqual(expect.arrayContaining(['厂商', '型号', '序列号', 'BMC', '组件健康']))
    expect(projectResourceDetail(detail('k8s_node')).flatMap((section) => section.fields).map((field) => field.label)).toEqual(expect.arrayContaining(['角色', '版本', 'Ready', '污点', '容量', '宿主']))
    expect(projectResourceDetail(detail('vm')).flatMap((section) => section.fields).map((field) => field.label)).toEqual(expect.arrayContaining(['Namespace', '节点', 'CPU', '内存', '磁盘', '网络', '迁移']))
  })

  it('uses 未提供 for absent fields and retains source in a dedicated quality field', () => {
    const projected = projectResourceDetail({ ...detail('vm'), data: { ...detail('vm').data, attributes: {} } })
    expect(projected.flatMap((section) => section.fields).find((field) => field.label === '迁移')?.value).toBe('未提供')
    expect(projected.flatMap((section) => section.fields).find((field) => field.key === 'source')?.value).toBe('inventory')
  })
})
