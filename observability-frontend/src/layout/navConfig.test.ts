import { describe, expect, it } from 'vitest'
import { LEGACY_REDIRECTS, PRIMARY_NAV, legacyTarget, visiblePrimaryNav } from './navConfig'

describe('workflow navigation contract', () => {
  it('keeps six workflow domains visible to operators', () => {
    expect(visiblePrimaryNav('operator').map((item) => item.label)).toEqual(['工作台', '调查', '资源', '观测', '处置', '报告'])
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
})
