import { describe, expect, it } from 'vitest'
import source from './App.tsx?raw'
import clusterSource from './pages/Clusters/ClusterOverview.tsx?raw'
import resourcesSource from './pages/Resources/index.tsx?raw'
import graphSource from './pages/observability/ResourceRelationships.tsx?raw'
import assistantSource from './pages/ai/AiChat.tsx?raw'
import knowledgeSource from './pages/Knowledge/index.tsx?raw'

describe('production shell', () => {
  it('does not expose a demo environment banner', () => {
    expect(source).not.toContain('演示环境')
  })

  it('exposes a stable semantic locator for each notification entry', () => {
    expect(source).toContain('data-testid="notification-alert-item"')
  })

  it('keeps the v3 product entry points stable', () => {
    for (const path of ['/overview', '/clusters/:clusterId', '/clusters/:clusterId/assistant', '/clusters/:clusterId/investigations', '/clusters/:clusterId/actions', '/clusters/:clusterId/knowledge', '/clusters/:clusterId/reports', '/admin']) {
      expect(source).toContain(path)
    }
  })

  it('does not expose global search or the fake production platform layer', () => {
    expect(source).not.toContain('全局搜索')
    expect(source).not.toContain('searchOpen')
    expect(source).not.toContain('生产平台')
  })

  it('keeps the platform overview readable before a cluster is selected', () => {
    expect(source).toContain("pathname !== '/overview'")
  })

  it('uses a query-preserving compatibility redirect for legacy routes', () => {
    expect(source).toContain('function LegacyRedirect')
    expect(source).toContain('new URLSearchParams(location.search)')
  })

  it('keeps canonical page headings aligned with the v3 route contract', () => {
    expect(clusterSource).toContain('title="集群详细总览"')
    expect(resourcesSource).toContain('title="资源目录"')
    expect(graphSource).toContain('title="资源关系图谱"')
    expect(assistantSource).toContain('title="智能运维助手"')
    expect(knowledgeSource).toContain('title="运维知识"')
  })
})
