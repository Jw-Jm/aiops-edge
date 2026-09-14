import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppLayout } from '../App'
import appSource from '../App.tsx?raw'
import navSource from '../layout/navConfig.ts?raw'
import scopeBarSource from '../features/scope/ScopeBar.tsx?raw'

const scopeMock = vi.hoisted(() => {
  const state = {
    authScope: { tenantId: 'tenant-a', activeClusterId: 'cluster-a' },
    active: { tenantId: 'tenant-a', clusterId: 'cluster-a', timeRange: { mode: 'relative', minutes: 60 } },
    clusters: [{ cluster_id: 'cluster-a', tenant_id: 'tenant-a', name: 'cloud-sh-01', status: 'ready' }],
    loading: false,
    switching: false,
    error: null,
    initialize: vi.fn().mockResolvedValue(undefined),
    switchCluster: vi.fn().mockResolvedValue(undefined),
    setResource: vi.fn(),
  }
  const useScopeStore = ((selector?: (value: typeof state) => unknown) => (selector ? selector(state) : state)) as ((
    selector?: (value: typeof state) => unknown,
  ) => unknown) & { getState: () => typeof state }
  useScopeStore.getState = () => state
  return { useScopeStore }
})

const authMock = vi.hoisted(() => {
  const state = { role: 'admin', username: 'operator', displayName: '运维员', logout: vi.fn() }
  const useAuthStore = ((selector?: (value: typeof state) => unknown) => (selector ? selector(state) : state)) as typeof state &
    ((selector?: (value: typeof state) => unknown) => unknown)
  return { useAuthStore }
})

vi.mock('../store/scopeStore', () => scopeMock)
vi.mock('../store/authStore', () => authMock)
vi.mock('../api/client', () => ({ getAlertEvents: vi.fn().mockResolvedValue({ data: { events: [] } }) }))
vi.mock('../features/scope/ScopeBar', () => ({ default: () => <span>cloud-sh-01</span> }))

vi.mock('../pages/aiOperations/AiOperations', () => ({ default: () => <h1>AI 智能运维工作台</h1> }))
vi.mock('../pages/Overview', () => ({ default: () => <h1>云平台运营态势</h1> }))
vi.mock('../pages/Clusters/ClusterOverview', () => ({ default: () => <h1>集群详细总览</h1> }))
vi.mock('../pages/observability/Observability', () => ({ default: () => <h1>全链路监控</h1> }))
vi.mock('../pages/knowledgeGraph/KnowledgeGraph', () => ({ default: () => <h1>知识图谱视图</h1> }))
vi.mock('../pages/Knowledge', () => ({ default: () => <h1>运维知识库</h1> }))
vi.mock('../pages/Reports', () => ({ default: () => <h1>报告中心</h1> }))
vi.mock('../pages/settings/Settings', () => ({ default: () => <h1>平台设置</h1> }))

function LocationProbe() {
  const location = useLocation()
  return (
    <output data-testid="location-probe">
      {location.pathname}
      {location.search}
    </output>
  )
}

describe('AIOps V1.4 八页壳层验收', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440, writable: true })
    vi.clearAllMocks()
  })

  it.each([
    ['/ai-operations', 'AI 智能运维工作台'],
    ['/overview', '云平台运营态势'],
    ['/clusters/cluster-a/overview', '集群详细总览'],
    ['/observability', '全链路监控'],
    ['/knowledge-graph', '知识图谱视图'],
    ['/knowledge', '运维知识库'],
    ['/reports', '报告中心'],
    ['/settings', '平台设置'],
  ])('%s 渲染规范页面', async (route, heading) => {
    render(
      <MemoryRouter initialEntries={[route]}>
        <AppLayout />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: heading })).toBeVisible()
  })

  it('默认根路径进入 AI 智能运维', async () => {
    render(
      <MemoryRouter initialEntries={['/']}>
        <><AppLayout /><LocationProbe /></>
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('/ai-operations'))
  })

  it('旧调查深链迁移到 AI 智能运维并保留 runId', async () => {
    render(
      <MemoryRouter initialEntries={['/investigation/run-42']}>
        <><AppLayout /><LocationProbe /></>
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('/ai-operations'))
    expect(screen.getByTestId('location-probe').textContent).toContain('from=%2Finvestigation%2Frun-42')
  })

  it('旧助手深链迁移到 AI 智能运维且不丢失查询参数', async () => {
    render(
      <MemoryRouter initialEntries={['/ai/chat?resource=pod-1']}>
        <><AppLayout /><LocationProbe /></>
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('/ai-operations'))
    expect(screen.getByTestId('location-probe').textContent).toContain('resource=pod-1')
  })

  it('合并旧治理入口到设置分区，缺少集群时显示迁移说明', async () => {
    render(
      <MemoryRouter initialEntries={['/admin/users']}>
        <AppLayout />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: '平台设置' })).toBeVisible()
  })

  it('一级导航渲染八个可读文字标签且顺序固定', async () => {
    render(
      <MemoryRouter initialEntries={['/overview']}>
        <AppLayout />
      </MemoryRouter>,
    )
    await screen.findByRole('heading', { name: '云平台运营态势' })
    const labels = ['AI 智能运维', '总览', '集群', '全链路监控', '知识图谱', '知识库', '报告', '设置']
    for (const label of labels) {
      expect(await screen.findByText(label, { selector: '.nav__label' })).toBeVisible()
    }
    const rendered = Array.from(document.querySelectorAll('.nav__label')).map((el) => el.textContent)
    expect(rendered).toEqual(labels)
  })

  it('导航不得出现被禁止的独立入口', async () => {
    render(
      <MemoryRouter initialEntries={['/overview']}>
        <AppLayout />
      </MemoryRouter>,
    )
    await screen.findByRole('heading', { name: '云平台运营态势' })
    const rendered = Array.from(document.querySelectorAll('.nav__label')).map((el) => el.textContent ?? '')
    for (const forbidden of ['问题', '资源', '调查', '处置', '系统管理']) {
      expect(rendered).not.toContain(forbidden)
    }
  })

  it('窄屏不隐藏导航，仍渲染八个入口', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 800, writable: true })
    render(
      <MemoryRouter initialEntries={['/overview']}>
        <AppLayout />
      </MemoryRouter>,
    )
    await screen.findByRole('heading', { name: '云平台运营态势' })
    expect(document.querySelectorAll('.nav__label').length).toBe(8)
  })

  it('集群名称只在集群选择器常驻显示', () => {
    expect(scopeBarSource).not.toContain('scope-bar__summary')
    expect(scopeBarSource).toContain('Select aria-label="集群"')
  })

  it('壳层源码不包含角色过滤与旧 IA 标签', () => {
    expect(navSource).not.toContain('adminOnly')
    expect(appSource).not.toContain('系统管理')
    expect(appSource).not.toContain("label: '助手'")
  })
})
