import { describe, expect, it } from 'vitest'
import { LEGACY_REDIRECTS, PRIMARY_NAV, clusterPath, legacyTarget, visiblePrimaryNav } from './navConfig'

describe('workflow navigation contract', () => {
  it('keeps the v3 workflow domains visible to operators', () => {
    expect(PRIMARY_NAV.map((item) => item.label)).toEqual(['平台', '集群', '助手', '调查', '处置', '知识', '报告', '系统管理'])
    expect(visiblePrimaryNav('operator').map((item) => item.label)).toEqual(['平台', '集群', '助手', '调查', '处置', '知识', '报告'])
  })

  it('keeps system administration admin-only', () => {
    const items = visiblePrimaryNav('admin')
    expect(items[items.length - 1]?.label).toBe('系统管理')
  })

  it('maps every legacy deep link to the workflow route', () => {
    expect(LEGACY_REDIRECTS.size).toBeGreaterThanOrEqual(14)
    expect(legacyTarget('/observability/service')).toBe('/resources?domain=application')
    expect(legacyTarget('/observability/relationships')).toBe('/resources?view=graph')
    expect(legacyTarget('/observability/vms')).toBe('/resources?domain=compute&type=vm')
    expect(legacyTarget('/infra/k8s')).toBe('/resources?domain=kubernetes')
    expect(legacyTarget('/hardware')).toBe('/resources?domain=compute&type=physical_server')
    expect(legacyTarget('/observability/trace')).toBe('/observe?view=traces')
    expect(legacyTarget('/admin/approvals')).toBe('/actions')
    expect(legacyTarget('/unknown')).toBeNull()
  })

  it('builds canonical cluster paths without inventing a production platform scope', () => {
    expect(clusterPath('cluster-1', 'overview')).toBe('/clusters/cluster-1')
    expect(clusterPath('cluster/1', 'knowledge')).toBe('/clusters/cluster%2F1/knowledge')
    expect(clusterPath('cluster-1', 'assistant')).toBe('/clusters/cluster-1/assistant')
  })
})
