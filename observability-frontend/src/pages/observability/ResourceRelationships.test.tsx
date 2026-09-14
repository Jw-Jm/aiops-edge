import { describe, expect, it } from 'vitest'
import source from './ResourceRelationships.tsx?raw'

describe('resource relationship explorer', () => {
  it('searches typed resources in the active cluster instead of a service-only graph', () => {
    expect(source).toContain('isDefaultGraphSearchType')
    expect(source).toContain('resourceDomainOf')
    expect(source).toContain('item.cluster_id === activeClusterId')
    expect(source).toContain('max_vertices: 300')
    expect(source).toContain('max_edges: 1000')
    expect(source).not.toContain("entity_type: 'service'")
  })

  it('uses the operations profile and never offers an application-service domain', () => {
    // 默认图谱搜索必须由服务端 operations profile 收窄，而不是前端先截断再过滤。
    expect(source).toContain("profile: 'operations'")
    expect(source).not.toContain('应用服务')
    // 兼容/仅详情类型不得成为默认搜索结果。
    expect(source).toContain('isDefaultGraphSearchType(item.entity_type)')
  })

  it('renders structured API errors as text instead of crashing React', () => {
    expect(source).toContain('function graphRequestErrorMessage')
    expect(source).toContain('typeof error.message === \'string\'')
    expect(source).toContain('setError(graphRequestErrorMessage(requestError))')
  })
})
