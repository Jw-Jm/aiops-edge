import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppLayout } from '../App'
import appSource from '../App.tsx?raw'
import adminSource from '../pages/admin/AdminSettings.tsx?raw'
import assistantSource from '../pages/ai/AiChat.tsx?raw'
import resourcesSource from '../pages/Resources/index.tsx?raw'
import aiDockSource from '../components/AiDock.tsx?raw'
import resourceCenterSource from '../pages/Resources/ResourceCenter.tsx?raw'

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
  const useScopeStore = ((selector?: (value: typeof state) => unknown) => selector ? selector(state) : state) as ((selector?: (value: typeof state) => unknown) => unknown) & { getState: () => typeof state }
  useScopeStore.getState = () => state
  return { useScopeStore }
})

const authMock = vi.hoisted(() => {
  const state = { role: 'admin', username: 'operator', displayName: '运维员', logout: vi.fn() }
  const useAuthStore = ((selector?: (value: typeof state) => unknown) => selector ? selector(state) : state) as typeof state & ((selector?: (value: typeof state) => unknown) => unknown)
  return { useAuthStore }
})

const uiMock = vi.hoisted(() => {
  const state = { collapsed: false, toggleCollapsed: vi.fn() }
  const useUIStore = ((selector?: (value: typeof state) => unknown) => selector ? selector(state) : state) as typeof state & ((selector?: (value: typeof state) => unknown) => unknown)
  return { useUIStore }
})

vi.mock('../store/scopeStore', () => scopeMock)
vi.mock('../store/authStore', () => authMock)
vi.mock('../store/uiStore', () => uiMock)
vi.mock('../api/client', () => ({ getAlertEvents: vi.fn().mockResolvedValue({ data: { events: [] } }) }))
vi.mock('../features/scope/ScopeBar', () => ({ default: () => <span>cloud-sh-01</span> }))

vi.mock('../pages/Overview', () => ({ default: () => <h1>云平台运营态势</h1> }))
vi.mock('../pages/Clusters/ClusterOverview', () => ({ default: () => <h1>集群详细总览</h1> }))
vi.mock('../pages/Resources', () => ({ default: () => <h1>资源目录</h1> }))
vi.mock('../pages/observability/ResourceRelationships', () => ({ default: () => <h1>资源关系图谱</h1> }))
vi.mock('../pages/ai/AiChat', () => ({ default: () => <h1>智能运维助手</h1> }))
vi.mock('../pages/Knowledge', () => ({ default: () => <h1>运维知识</h1> }))

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-probe">{location.pathname}{location.search}</output>
}

describe('AIOps UI v3 cross-page acceptance', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440, writable: true })
    vi.clearAllMocks()
  })

  it.each([
    ['/overview', '云平台运营态势'],
    ['/clusters/cluster-a', '集群详细总览'],
    ['/clusters/cluster-a/resources', '资源目录'],
    ['/clusters/cluster-a/graph', '资源关系图谱'],
    ['/clusters/cluster-a/assistant', '智能运维助手'],
    ['/clusters/cluster-a/knowledge', '运维知识'],
  ])('%s renders canonical heading', async (route, heading) => {
    render(<MemoryRouter initialEntries={[route]}><AppLayout /></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: heading })).toBeVisible()
  })

  it('keeps the visible product boundary free of environment selectors and composite health scores', async () => {
    const source = [appSource, adminSource, assistantSource, resourcesSource, aiDockSource].join('\n')
    expect(source).not.toContain('生产平台')
    expect(source).not.toContain('production-cluster')
    expect(source).not.toContain('local / production')
    expect(source).not.toContain('分析 prod 集群故障根因')
    expect(source).not.toContain('综合健康分数')
    expect(source).not.toContain('Workload 合并')
    expect(appSource).toContain('navRailWidth')
    expect(appSource).not.toContain('width: isCollapsed ? 64 : 216')
    expect(resourceCenterSource).toContain('entityUid')
    expect(resourceCenterSource).toContain('useNavigate')
    expect(resourcesSource).toContain('resources/${encodeURIComponent')
  })

  it('redirects the legacy assistant deep link without dropping the query', async () => {
    render(<MemoryRouter initialEntries={['/ai/chat?resource=pod-1']}><><AppLayout /><LocationProbe /></></MemoryRouter>)
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('/clusters/cluster-a/assistant?resource=pod-1'))
  })

  it('keeps platform and cluster health readable below the full desktop width', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 800, writable: true })
    render(<MemoryRouter initialEntries={['/overview']}><AppLayout /></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: '云平台运营态势' })).toBeVisible()
  })
})
