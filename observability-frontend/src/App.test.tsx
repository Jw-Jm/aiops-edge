import { describe, expect, it } from 'vitest'
import source from './App.tsx?raw'
import navSource from './layout/navConfig.ts?raw'

describe('V1.4 生产壳层合同', () => {
  it('不暴露演示环境横幅', () => {
    expect(source).not.toContain('演示环境')
    expect(source).not.toContain('生产平台')
  })

  it('为每个通知条目保留稳定语义定位器', () => {
    expect(source).toContain('data-testid="notification-alert-item"')
  })

  it('八个规范路由全部注册', () => {
    for (const path of [
      '/ai-operations',
      '/overview',
      '/clusters/:clusterUid/overview',
      '/observability',
      '/knowledge-graph',
      '/knowledge',
      '/reports',
      '/settings',
    ]) {
      expect(source).toContain(path)
    }
  })

  it('根路径默认进入 AI 智能运维（第一入口）', () => {
    expect(source).toContain('AI_OPERATIONS_PATH')
    expect(source).toContain('<Route path="/" element={<Navigate replace to={AI_OPERATIONS_PATH} />} />')
  })

  it('不再存在问题/资源/调查/处置的独立路由入口', () => {
    const legacyOnly = ['/clusters/:clusterId/investigations', '/clusters/:clusterId/actions', '/clusters/:clusterId/resources', '/clusters/:clusterId/assistant']
    for (const path of legacyOnly) {
      expect(source).not.toContain(`<Route path="${path}"`)
    }
  })

  it('旧路由通过统一迁移组件处理，不静默跳转', () => {
    expect(source).toContain('resolveLegacyRoute')
    expect(source).toContain('LegacyOrNotFound')
    expect(source).toContain('MigrationNotice')
  })

  it('统一账户：不再按角色过滤导航', () => {
    expect(source).not.toContain('visiblePrimaryNav(role)')
    expect(navSource).not.toContain('adminOnly')
    expect(source).not.toContain('adminOnly')
  })

  it('顶栏保留数据截止、时区与质量表达入口（ScopeBar）', () => {
    expect(source).toContain('<ScopeBar')
  })

  it('无全局搜索与伪造平台层', () => {
    expect(source).not.toContain('全局搜索')
    expect(source).not.toContain('searchOpen')
  })
})
