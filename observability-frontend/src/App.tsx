import React, { useState, lazy, Suspense, useEffect } from 'react'
import { Routes, Route, Navigate, useNavigate, useLocation, useParams } from 'react-router-dom'
import { Alert, Dropdown, Spin } from 'antd'
import { useScopeStore } from './store/scopeStore'
import { useAuthStore } from './store/authStore'
import AppIcon from './components/AppIcons'
import RequireAuth from './components/RequireAuth'
import { getAlertEvents } from './api/client'
import ScopeBar from './features/scope/ScopeBar'
import { LEGACY_REDIRECTS, clusterPath, visiblePrimaryNav } from './layout/navConfig'

// ===== 懒加载页面（全新 IA）=====
const Login = lazy(() => import('./pages/Login'))
const ChangePassword = lazy(() => import('./pages/ChangePassword'))
const Overview = lazy(() => import('./pages/Overview'))
const ClusterOverview = lazy(() => import('./pages/Clusters/ClusterOverview'))
const ServiceObservability = lazy(() => import('./pages/observability/ServiceObservability'))
const Trace = lazy(() => import('./pages/observability/Trace'))
const LogMetrics = lazy(() => import('./pages/observability/LogMetrics'))
const VirtualMachines = lazy(() => import('./pages/observability/VirtualMachines'))
const AlertEvents = lazy(() => import('./pages/alerts/AlertEvents'))
const AlertRules = lazy(() => import('./pages/alerts/AlertRules'))
const AiChat = lazy(() => import('./pages/ai/AiChat'))
const Capacity = lazy(() => import('./pages/capacity/Capacity'))
const Report = lazy(() => import('./pages/report/Report'))
const AdminUsers = lazy(() => import('./pages/admin/AdminUsers'))
const Approvals = lazy(() => import('./pages/admin/Approvals'))
const AdminSettings = lazy(() => import('./pages/admin/AdminSettings'))
const K8sActions = lazy(() => import('./pages/infra/K8sActions'))
const Grafana = lazy(() => import('./pages/observability/Grafana'))
// P2-4 新增页面：硬件健康 / 变更时间线
const Hardware = lazy(() => import('./pages/infra/Hardware'))
const Changes = lazy(() => import('./pages/infra/Changes'))
// P12：调查中心 / 智能调查 / 发起调查（六大导航收敛，AI Chat 不再顶层）
const InvestigationCenter = lazy(() => import('./pages/investigation/InvestigationCenter'))
const IntelligentInvestigation = lazy(() => import('./pages/investigation/IntelligentInvestigation'))
const NewInvestigation = lazy(() => import('./pages/investigation/NewInvestigation'))
// Evidence 详情深链（tenant+cluster+run 三元授权，只读）
const EvidenceDetail = lazy(() => import('./pages/investigation/EvidenceDetail'))
const ResourceRelationships = lazy(() => import('./pages/observability/ResourceRelationships'))
const GraphOperations = lazy(() => import('./pages/admin/GraphOperations'))
const Resources = lazy(() => import('./pages/Resources'))
const Observe = lazy(() => import('./pages/Observe'))
const Actions = lazy(() => import('./pages/Actions'))
const Reports = lazy(() => import('./pages/Reports'))
const Knowledge = lazy(() => import('./pages/Knowledge'))
const Admin = lazy(() => import('./pages/admin/AdminHome'))
const NotFound = lazy(() => import('./pages/NotFound'))
// Stable product route contract: platform overview is global; every other
// primary workspace is rooted at a canonical cluster path.

function LegacyRedirect({ target }: { target: string }) {
  const location = useLocation()
  const [pathname, targetSearch = ''] = target.split('?')
  const merged = new URLSearchParams(location.search)
  new URLSearchParams(targetSearch).forEach((value, key) => merged.set(key, value))
  const query = merged.toString()
  return <Navigate replace to={`${pathname}${query ? `?${query}` : ''}`} />
}

function LegacyAssistantRedirect() {
  const location = useLocation()
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const target = activeClusterId ? clusterPath(activeClusterId, 'assistant') : '/clusters'
  const query = location.search
  return <Navigate replace to={`${target}${query}`} />
}

function ClusterEntry() {
  const navigate = useNavigate()
  const clusters = useScopeStore((state) => state.clusters)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const loading = useScopeStore((state) => state.loading)
  if (activeClusterId) return <Navigate replace to={clusterPath(activeClusterId, 'overview')} />
  return (
    <section className="scope-required" aria-labelledby="cluster-selection-title">
      <h1 id="cluster-selection-title">选择集群</h1>
      <p>请选择一个已授权的 Kubernetes 集群进入详细总览。</p>
      {loading ? <Spin /> : clusters.length === 0 ? <Alert type="warning" showIcon message="暂无可用集群" /> : (
        <div className="scope-required__list">
          {clusters.map((cluster) => (
            <button key={cluster.cluster_id} type="button" onClick={() => navigate(`/clusters/${encodeURIComponent(cluster.cluster_id)}`)}>
              <strong>{cluster.name}</strong><span>{cluster.cluster_id}</span>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}

function ClusterRouteGate({ children }: { children: React.ReactNode }) {
  const { clusterId = '' } = useParams<{ clusterId: string }>()
  const clusters = useScopeStore((state) => state.clusters)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const switching = useScopeStore((state) => state.switching)
  const error = useScopeStore((state) => state.error)
  const switchCluster = useScopeStore((state) => state.switchCluster)
  const [invalid, setInvalid] = useState(false)

  useEffect(() => {
    const authorized = clusters.some((cluster) => cluster.cluster_id === clusterId)
    if (!clusterId || (clusters.length > 0 && !authorized)) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    if (activeClusterId && activeClusterId !== clusterId && !switching) {
      void switchCluster(clusterId).catch(() => undefined)
    }
  }, [activeClusterId, clusterId, clusters, switchCluster, switching])

  if (invalid) return <Alert type="error" showIcon message="无权访问该集群" description="当前登录用户没有该 Kubernetes 集群的访问权限。" />
  if (error && activeClusterId !== clusterId) return <Alert type="error" showIcon message="集群作用域切换失败" description={error} />
  if (activeClusterId !== clusterId || switching) return <div className="route-loading"><Spin /></div>
  return <>{children}</>
}

function ClusterOverviewPlaceholder() {
  const { clusterId = '' } = useParams<{ clusterId: string }>()
  return <section className="page-header"><div><h1 className="page-title">集群详细总览</h1><p className="page-desc">当前集群：{clusterId}</p></div></section>
}

export function AppLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const initializeScope = useScopeStore((s) => s.initialize)
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const scopeLoading = useScopeStore((s) => s.loading)
  const scopeError = useScopeStore((s) => s.error)
  const logout = useAuthStore((s) => s.logout)
  const [compact, setCompact] = useState(false)
  const [narrow, setNarrow] = useState(false)
  const [alertCount, setAlertCount] = useState<number | null>(null)
  // 修复 5.7：通知抽屉需要最近告警明细，与 alertCount 一并拉取
  const [recentAlerts, setRecentAlerts] = useState<any[]>([])

  // 初始化服务端 AuthScope；没有 active cluster 时页面保持 ScopeRequired。
  useEffect(() => {
    void initializeScope()
  }, [initializeScope])

  // P3-1: 侧栏告警 badge 动态拉取真实告警数（替代硬编码 12）
  useEffect(() => {
    const loadAlerts = () => {
      if (!activeClusterId) return
      getAlertEvents({ limit: 200 }).then((r) => {
        const d = r.data
        // 通知抽屉：优先取 events 数组，兼容分页对象
        const list = Array.isArray(d) ? d : (d?.events ?? d?.data ?? [])
        const events = Array.isArray(list) ? list : []
        // 修复(P2)：侧栏角标只统计未解决的告警展开行数（status != resolved），
        // 否则所有历史已 resolve 的事件仍会显示在角标中，让用户误以为"还在告警"。
        // 同步按展开行数（每条告警的 object 字段按","拆分多行）统计，与表格对齐。
        const activeEvents = events.filter((e: any) => e.status !== 'resolved')
        const expandedCount = activeEvents.reduce((sum: number, e: any) => {
          const objs = (e?.object || '').split(',').map((s: string) => s.trim()).filter(Boolean)
          return sum + (objs.length || 1)
        }, 0)
        setAlertCount(expandedCount)
        setRecentAlerts(activeEvents)
      }).catch(() => { setAlertCount(null); setRecentAlerts([]) })
    }
    loadAlerts()
    const t = setInterval(loadAlerts, 30000) // 30s 刷新
    return () => clearInterval(t)
  }, [activeClusterId])

  useEffect(() => {
    const updateViewport = () => {
      setCompact(window.innerWidth <= 1280)
      setNarrow(window.innerWidth < 1024)
    }
    updateViewport()
    window.addEventListener('resize', updateViewport)
    return () => window.removeEventListener('resize', updateViewport)
  }, [])

  // 高亮当前路由
  const pathname = location.pathname
  const auth = useAuthStore()
  const role = auth.role
  // PF-LOGIC-013: 侧栏按 role 过滤后的导航组
  const visibleNav = visiblePrimaryNav(role)
  // The IA uses a navigation rail at every desktop breakpoint. Keep the rail
  // narrow so the product surface, rather than a legacy expanded sidebar,
  // owns the available width.
  const navRailWidth = narrow ? 64 : compact ? 72 : 88
  const narrowReadOnlyRoute = pathname === '/overview' || /^\/clusters\/[^/]+\/?$/.test(pathname)
  const selectedItem = visibleNav.find((item) => {
    if (item.workspace) {
      if (!pathname.startsWith('/clusters/')) return false
      return item.workspace === 'overview'
        ? /^\/clusters\/[^/]+\/?$/.test(pathname)
        : pathname.includes(`/${item.workspace}`)
    }
    return pathname === item.path || pathname.startsWith(`${item.path}/`)
  }) ?? visibleNav.find((item) => item.path === '/overview')
  const navigateToItem = (item: (typeof visibleNav)[number]) => {
    if (!item.workspace) {
      navigate(item.path)
      return
    }
    navigate(activeClusterId ? clusterPath(activeClusterId, item.workspace) : '/clusters')
  }

  const userLabel = auth.displayName || auth.username || '用户'
  const displayName = userLabel.slice(0, 1).toUpperCase()

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--bg)' }}>
      {/* 侧栏 */}
      <aside className="sidebar" style={{ width: navRailWidth, flexShrink: 0, transition: 'width .2s' }}>
        <div className="brand">
          <div className="brand__logo">观</div>
        </div>

        <div className="sidebar__scroll">
          <nav className="nav">
            {/* 每个一级导航项固定渲染图标 + 11px 文字标签，不依赖 hover title 才能识别。 */}
            {visibleNav.map((it) => (
              <div key={`${it.path}:${it.workspace ?? ''}`} className={'nav__item' + (selectedItem === it ? ' is-active' : '')}
                onClick={() => navigateToItem(it)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); navigateToItem(it) } }} role="button" tabIndex={0} aria-label={it.label}>
                <AppIcon name={it.icon} />
                <span className="nav__label">{it.label}</span>
                {it.badge && (
                  <span className="nav__badge">
                    {it.badge === 'dynamic' ? (alertCount ?? '') : it.badge}
                  </span>
                )}
              </div>
            ))}
          </nav>
        </div>

      </aside>

      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        {/* 顶栏 */}
        <header className="topbar">
          <div className="topbar__scope"><ScopeBar /></div>
          <div className="topbar__spacer" />
          {/* 修复 5.7：通知按钮从"直接跳转告警页"改为下拉抽屉，展示最近告警，点击进入告警事件页 */}
          <Dropdown
            trigger={['click']}
            dropdownRender={() => (
              <div style={{ width: 340, background: 'var(--surface-1)', borderRadius: 12, boxShadow: 'var(--shadow-lg)', border: '1px solid var(--border)', overflow: 'hidden' }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 14px', borderBottom: '1px solid var(--border-soft)' }}>
                  <span style={{ fontWeight: 600, fontSize: 13 }}>告警通知</span>
                  <span onClick={() => navigate('/alerts/events')} style={{ fontSize: 12, color: 'var(--primary)', cursor: 'pointer' }}>查看全部 →</span>
                </div>
                <div style={{ maxHeight: 320, overflowY: 'auto' }}>
                  {(recentAlerts || []).length === 0 ? (
                    <div style={{ padding: 20, textAlign: 'center', color: 'var(--text-muted)', fontSize: 12 }}>暂无告警</div>
                  ) : (recentAlerts || []).slice(0, 6).map((a: any) => (
                    <div key={a.id || a.alert_id || a.title} onClick={() => navigate(`/alerts/events`)}
                      data-testid="notification-alert-item"
                      style={{ display: 'flex', gap: 10, padding: '10px 14px', borderBottom: '1px solid var(--border-soft)', cursor: 'pointer', alignItems: 'flex-start' }}>
                      <span style={{ width: 8, height: 8, borderRadius: '50%', marginTop: 5, flexShrink: 0, background: a.severity === 'critical' ? 'var(--danger)' : a.severity === 'warning' ? 'var(--warning)' : 'var(--primary)' }} />
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: 'var(--text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.rule_name || a.title || a.summary || a.alert_name || '告警'}</div>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>{(a.object || a.service_name || a.service || a.cluster_id || '')}{a.created_at ? ` · ${String(a.created_at).slice(5, 16).replace('T', ' ')}` : ''}</div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          >
            <div className="topbar__icon-btn" title="通知">
              {Number(alertCount || 0) > 0 && <span className="ping" />}
              <AppIcon name="bell" />
            </div>
          </Dropdown>
          <Dropdown
            menu={{
              items: [
                { key: 'profile', label: '个人资料', disabled: true },
                // PF-UI-004: 启用修改密码入口，跳转独立修改密码页
                { key: 'password', label: '修改密码', onClick: () => navigate('/change-password') },
                { type: 'divider' },
                { key: 'settings', label: '系统设置', onClick: () => navigate('/admin/settings') },
                { key: 'about', label: '关于平台', disabled: true },
                { type: 'divider' },
                { key: 'logout', label: '退出登录', danger: true, onClick: () => { logout(); navigate('/login'); window.location.reload() } },
              ],
            }}
          >
            <div className="user-chip" style={{ cursor: 'pointer' }}>
              <div className="avatar">{displayName}</div>
              <span className="text-sm">{userLabel}</span>
            </div>
          </Dropdown>
        </header>

        {/* 内容区 */}
        <main style={{ flex: 1, padding: '20px 24px', overflow: 'auto', minHeight: 0 }}>
          {!activeClusterId && pathname !== '/overview' && !scopeLoading ? (
            <Alert
              showIcon
              type={scopeError ? 'error' : 'warning'}
              message={scopeError ? '作用域加载失败' : '请选择作用域'}
              description={scopeError || '当前页面不会展示跨集群混合数据。请先在右上角选择一个服务端已授权的集群。'}
              style={{ marginBottom: 16 }}
            />
          ) : null}
          {narrow && !narrowReadOnlyRoute ? (
            <div className="production-width-gate" role="alert" data-testid="production-width-gate">
              当前窗口宽度不足 1024px，生产操作仅在只读模式下可用。请将窗口扩大后继续。
            </div>
          ) : <Suspense fallback={<div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>}>
            <Routes>
              {Array.from(LEGACY_REDIRECTS.entries()).map(([from, to]) => <Route key={from} path={from} element={<LegacyRedirect target={to} />} />)}
              <Route path="/clusters" element={<ClusterEntry />} />
              <Route path="/clusters/:clusterId" element={<ClusterRouteGate><ClusterOverview /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/resources" element={<ClusterRouteGate><Resources /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/resources/:entityUid" element={<ClusterRouteGate><Resources /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/observe" element={<ClusterRouteGate><Observe /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/graph" element={<ClusterRouteGate><ResourceRelationships /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/assistant" element={<ClusterRouteGate><AiChat /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/investigations" element={<ClusterRouteGate><InvestigationCenter /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/actions" element={<ClusterRouteGate><Actions /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/knowledge" element={<ClusterRouteGate><Knowledge /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/knowledge/new" element={<ClusterRouteGate><Knowledge /></ClusterRouteGate>} />
              <Route path="/clusters/:clusterId/reports" element={<ClusterRouteGate><Reports /></ClusterRouteGate>} />
              <Route path="/ai/chat" element={<LegacyAssistantRedirect />} />
              <Route path="/overview" element={<Overview />} />
              <Route path="/resources" element={<Resources />} />
              <Route path="/observe" element={<Observe />} />
              <Route path="/actions" element={<Actions />} />
              <Route path="/reports" element={<Reports />} />
              <Route path="/admin" element={<Admin />} />
              <Route path="/observability/service" element={<ServiceObservability />} />
              <Route path="/observability/relationships" element={<ResourceRelationships />} />
              <Route path="/observability/trace" element={<Trace />} />
              <Route path="/observability/log" element={<LogMetrics />} />
              <Route path="/observability/vms" element={<VirtualMachines />} />
              <Route path="/alerts/events" element={<AlertEvents />} />
              <Route path="/alerts/rules" element={<AlertRules />} />
              {/* P12：调查中心 / 智能调查 / 发起调查 */}
              <Route path="/investigation" element={<InvestigationCenter />} />
              <Route path="/investigation/new" element={<NewInvestigation />} />
              <Route path="/investigation/:runId" element={<IntelligentInvestigation />} />
              <Route path="/investigation/:runId/evidence/:evidenceId" element={<EvidenceDetail />} />

              <Route path="/capacity" element={<Capacity />} />
              <Route path="/infra/k8s" element={<K8sActions />} />
              <Route path="/hardware" element={<Hardware />} />
              <Route path="/changes" element={<Changes />} />
              <Route path="/observability/grafana" element={<Grafana />} />
              <Route path="/report" element={<Report />} />
              <Route path="/admin/approvals" element={<Approvals />} />
              <Route path="/admin/users" element={<AdminUsers />} />
              <Route path="/admin/settings" element={<AdminSettings />} />
              <Route path="/admin/graph-operations" element={<GraphOperations />} />
              <Route path="/" element={<Overview />} />
              <Route path="*" element={<NotFound />} />
            </Routes>
          </Suspense>}
        </main>
      </div>

    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Suspense fallback={<Spin />}><Login /></Suspense>} />
      <Route path="/change-password" element={<RequireAuth><Suspense fallback={<Spin />}><ChangePassword /></Suspense></RequireAuth>} />
      <Route path="*" element={<RequireAuth><AppLayout /></RequireAuth>} />
    </Routes>
  )
}
