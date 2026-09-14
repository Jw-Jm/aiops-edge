import { describe, expect, it } from 'vitest'
import {
  PRIMARY_NAV,
  SETTINGS_SECTIONS,
  clusterPath,
  isPrimaryNavActive,
  parseClusterRoute,
  resolveLegacyRoute,
  visiblePrimaryNav,
} from './navConfig'

const FORBIDDEN_LABELS = ['问题', '资源', '调查', '处置', 'AI 能力中心', '图谱运维', '接入目录', '平台健康', '安全管理']

describe('V1.4 八页信息架构契约', () => {
  it('主导航恰好八项且顺序固定，AI 智能运维为第一入口', () => {
    expect(PRIMARY_NAV.map((item) => item.label)).toEqual([
      'AI 智能运维',
      '总览',
      '集群',
      '全链路监控',
      '知识图谱',
      '知识库',
      '报告',
      '设置',
    ])
    expect(PRIMARY_NAV[0].path).toBe('/ai-operations')
  })

  it('规范路由与设计文档 §4.1 一致', () => {
    expect(PRIMARY_NAV.map((item) => item.path)).toEqual([
      '/ai-operations',
      '/overview',
      '/clusters/:clusterUid/overview',
      '/observability',
      '/knowledge-graph',
      '/knowledge',
      '/reports',
      '/settings',
    ])
  })

  it('不保留问题/资源/调查/处置等独立入口', () => {
    const labels = PRIMARY_NAV.map((item) => item.label)
    for (const forbidden of FORBIDDEN_LABELS) {
      expect(labels).not.toContain(forbidden)
    }
  })

  it('统一账户：任何角色看到同一套完整导航（无 adminOnly 隐藏）', () => {
    expect(visiblePrimaryNav('viewer').map((i) => i.label)).toEqual(PRIMARY_NAV.map((i) => i.label))
    expect(visiblePrimaryNav('operator').map((i) => i.label)).toEqual(PRIMARY_NAV.map((i) => i.label))
    expect(visiblePrimaryNav('admin').map((i) => i.label)).toEqual(PRIMARY_NAV.map((i) => i.label))
    expect(visiblePrimaryNav(undefined).length).toBe(8)
    // 源码层面不得再出现角色过滤
    expect(PRIMARY_NAV.some((item) => 'adminOnly' in item)).toBe(false)
  })

  it('设置页分区覆盖设计文档 §4.4 的十个分区', () => {
    expect(SETTINGS_SECTIONS.map((s) => s.id)).toEqual([
      'overview',
      'integrations',
      'graph',
      'llm',
      'workflow',
      'mcp',
      'rag',
      'security',
      'health',
      'evaluation',
    ])
  })

  it('集群页使用 canonical clusterUid 且路径可解析', () => {
    expect(clusterPath('91771a6e-9c2d-11f1-8271-bea176fe9f9f')).toBe(
      '/clusters/91771a6e-9c2d-11f1-8271-bea176fe9f9f/overview',
    )
    expect(clusterPath('cluster/1')).toBe('/clusters/cluster%2F1/overview')
    expect(parseClusterRoute('/clusters/abc%2F1/overview')).toEqual({ clusterUid: 'abc/1', section: 'overview' })
  })

  it('路由高亮：集群页高亮集群，图谱不与知识库冲突', () => {
    const clusters = PRIMARY_NAV.find((i) => i.id === 'clusters')!
    const knowledge = PRIMARY_NAV.find((i) => i.id === 'knowledge')!
    const graph = PRIMARY_NAV.find((i) => i.id === 'knowledge-graph')!
    expect(isPrimaryNavActive('/clusters/abc/overview', clusters)).toBe(true)
    expect(isPrimaryNavActive('/knowledge-graph', graph)).toBe(true)
    expect(isPrimaryNavActive('/knowledge', knowledge)).toBe(true)
    // 图谱路由必须优先命中最具体的条目
    const matched = PRIMARY_NAV.filter((i) => isPrimaryNavActive('/knowledge-graph', i))
    expect(matched.map((i) => i.id)).toContain('knowledge-graph')
  })
})

describe('附录 A 旧路由迁移表', () => {
  it('根路径与旧 AI 入口迁移到 AI 智能运维', () => {
    expect(resolveLegacyRoute('/', '')?.target.startsWith('/ai-operations')).toBe(true)
    expect(resolveLegacyRoute('/ai/chat', '')?.target.startsWith('/ai-operations')).toBe(true)
    expect(resolveLegacyRoute('/investigation/run-1', '')?.target).toContain('from=%2Finvestigation%2Frun-1')
  })

  it('旧观测入口迁移到全链路监控并保留 signal', () => {
    const r = resolveLegacyRoute('/observability/trace', 'signal=traces')
    expect(r?.target).toContain('/observability')
    expect(r?.target).toContain('signal=traces')
  })

  it('旧告警入口按 scope 迁移且不保留独立问题页', () => {
    const r = resolveLegacyRoute('/alerts/events', 'alert=abc')
    expect(r?.target.startsWith('/overview')).toBe(true)
    expect(r?.target).toContain('alert=abc')
  })

  it('旧集群资源入口解析为 canonical clusterUid 的集群页', () => {
    const r = resolveLegacyRoute('/clusters/cluster-1/resources', 'domain=compute')
    expect(r?.target).toContain('/clusters/cluster-1/overview')
    expect(r?.target).toContain('tab=resources')
    expect(r?.target).toContain('domain=compute')
  })

  it('无法解析 clusterUid 时标记 partial 并显示迁移说明', () => {
    const r = resolveLegacyRoute('/capacity', '')
    expect(r?.convertible).toBe(false)
    expect(r?.target).toContain('migration=partial')
  })

  it('旧治理入口统一迁移到设置分区', () => {
    expect(resolveLegacyRoute('/admin', '')?.target).toBe('/settings?from=%2Fadmin')
    expect(resolveLegacyRoute('/admin/users', '')?.target).toContain('/settings?section=security')
    expect(resolveLegacyRoute('/admin/graph-operations', '')?.target).toContain('/settings?section=graph')
    expect(resolveLegacyRoute('/components', '')?.target).toContain('/settings?section=health')
  })

  it('未知路径不产生静默跳转', () => {
    expect(resolveLegacyRoute('/definitely-not-a-route', '')).toBeNull()
  })
})
