import React, { useState, lazy, Suspense, useEffect } from 'react'
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom'
import { Alert, Dropdown, Spin } from 'antd'
import { useScopeStore } from './store/scopeStore'
import { useAuthStore } from './store/authStore'
import AppIcon from './components/AppIcons'
import RequireAuth from './components/RequireAuth'
import { getAlertEvents } from './api/client'
import ScopeBar from './features/scope/ScopeBar'
import {
  AI_OPERATIONS_PATH,
  PRIMARY_NAV,
  clusterPath,
  isPrimaryNavActive,
  parseClusterRoute,
  resolveLegacyRoute,
} from './layout/navConfig'

// ===== 八个一级页面（V1.4 唯一信息架构）=====
const AiOperations = lazy(() => import('./pages/aiOperations/AiOperations'))
const Overview = lazy(() => import('./pages/Overview'))
const ClusterOverview = lazy(() => import('./pages/Clusters/ClusterOverview'))
const Observability = lazy(() => import('./pages/observability/Observability'))
const KnowledgeGraph = lazy(() => import('./pages/knowledgeGraph/KnowledgeGraph'))
const Knowledge = lazy(() => import('./pages/Knowledge'))
const Reports = lazy(() => import('./pages/Reports'))
const Settings = lazy(() => import('./pages/settings/Settings'))

// 认证相关（非一级页面）
const Login = lazy(() => import('./pages/Login'))
const ChangePassword = lazy(() => import('./pages/ChangePassword'))
const NotFound = lazy(() => import('./pages/NotFound'))

/**
 * 旧路由迁移（附录 A）：保留可转换参数，无法转换时显示迁移说明；
 * 未知路径不静默跳转，直接进入 404。
 */
function LegacyOrNotFound() {
  const location = useLocation()
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const result = resolveLegacyRoute(location.pathname, location.search, activeClusterId || null)
  if (!result) return <NotFound />
  return <Navigate replace to={result.target} />
}

/** 无法无损迁移时在页面顶部显示的迁移说明（§4.3 无效参数/旧路由） */
function MigrationNotice() {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  const from = params.get('from')
  if (!from) return null
  const partial = params.get('migration') === 'partial'
  return (
    <Alert
      type={partial ? 'warning' : 'info'}
      showIcon
      closable
      data-testid="migration-notice"
      style={{ marginBottom: 12 }}
      message={partial ? '部分旧参数无法自动转换' : '已从旧入口迁移'}
      description={
        partial
          ? `原入口 ${from} 无法完全映射到当前页面，已按安全默认视图打开。缺失的筛选条件需要重新选择。`
          : `原入口 ${from} 已迁移到规范路由，可转换的筛选与范围参数已保留。`
      }
    />
  )
}

function ClusterEntry() {
  const navigate = useNavigate()
  const clusters = useScopeStore((state) => state.clusters)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const loading = useScopeStore((state) => state.loading)
  if (activeClusterId) return <Navigate replace to={clusterPath(activeClusterId)} />
  return (
    <section className="scope-required" aria-labelledby="cluster-selection-title">
      <h1 id="cluster-selection-title">选择集群</h1>
      <p>请选择一个已授权的 Kubernetes 集群进入集群总览。</p>
      {loading ? (
        <Spin />
      ) : clusters.length === 0 ? (
        <Alert type="warning" showIcon message="暂无可用集群" description="当前账户没有被授权任何集群，请联系运维管理员完成接入。" />
      ) : (
        <div className="scope-required__list">
          {clusters.map((cluster) => (
            <button key={cluster.cluster_id} type="button" onClick={() => navigate(clusterPath(cluster.cluster_id))}>
              <strong>{cluster.name}</strong>
              <span>{cluster.cluster_id}</span>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}

function ClusterRouteGate({ children }: { children: React.ReactNode }) {
  const clusters = useScopeStore((state) => state.clusters)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const switching = useScopeStore((state) => state.switching)
  const error = useScopeStore((state) => state.error)
  const switchCluster = useScopeStore((state) => state.switchCluster)
  const location = useLocation()
  const route = parseClusterRoute(location.pathname)
  const clusterUid = route?.clusterUid ?? ''
  const [invalid, setInvalid] = useState(false)

  useEffect(() => {
    const authorized = clusters.some((cluster) => cluster.cluster_id === clusterUid)
    if (!clusterUid || (clusters.length > 0 && !authorized)) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    if (activeClusterId && activeClusterId !== clusterUid && !switching) {
      void switchCluster(clusterUid).catch(() => undefined)
    }
  }, [activeClusterId, clusterUid, clusters, switchCluster, switching])

  if (invalid) {
    return <Alert type="error" showIcon message="无权访问该集群" description="当前登录账户没有该 Kubernetes 集群的访问权限。" />
  }
  if (error && activeClusterId !== clusterUid) return <Alert type="error" showIcon message="集群作用域切换失败" description={error} />
  if (activeClusterId !== clusterUid || switching) return <div className="route-loading"><Spin /></div>
  return <>{children}</>
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
  const [recentAlerts, setRecentAlerts] = useState<any[]>([])
  const [scopeVersion, setScopeVersion] = useState(0)

  useEffect(() => {
    void initializeScope()
  }, [initializeScope])

  // 侧栏徽标：只统计未解决告警展开行数（有明确查询口径），不做无口径总数
  useEffect(() => {
    const loadAlerts = () => {
      if (!activeClusterId) return
      getAlertEvents({ limit: 200 })
        .then((r) => {
          const d = r.data
          const list = Array.isArray(d) ? d : (d?.events ?? d?.data ?? [])
          const events = Array.isArray(list) ? list : []
          const activeEvents = events.filter((e: any) => e.status !== 'resolved')
          const expandedCount = activeEvents.reduce((sum: number, e: any) => {
            const objs = (e?.object || '').split(',').map((s: string) => s.trim()).filter(Boolean)
            return sum + (objs.length || 1)
          }, 0)
          setAlertCount(expandedCount)
          setRecentAlerts(activeEvents)
        })
        .catch(() => {
          setAlertCount(null)
          setRecentAlerts([])
        })
    }
    loadAlerts()
    const t = setInterval(loadAlerts, 30000)
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

  const pathname = location.pathname
  const auth = useAuthStore()
  const navRailWidth = narrow ? 64 : compact ? 72 : 88

  const navigateToItem = (item: (typeof PRIMARY_NAV)[number]) => {
    if (item.requiresCluster) {
      navigate(activeClusterId ? clusterPath(activeClusterId) : '/clusters')
      return
    }
    navigate(item.path)
  }

  const userLabel = auth.displayName || auth.username || '用户'
  const displayName = userLabel.slice(0, 1).toUpperCase()
  const currentPage = PRIMARY_NAV.find((item) => isPrimaryNavActive(pathname, item)) ?? PRIMARY_NAV[0]

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--bg)' }}>
      <aside className="sidebar" style={{ width: navRailWidth, flexShrink: 0, transition: 'width .2s' }}>
        <div className="brand">
          <div className="brand__logo">观</div>
        </div>
        <div className="sidebar__scroll">
          <nav className="nav" aria-label="一级导航">
            {PRIMARY_NAV.map((item) => (
              <div
                key={item.id}
                className={'nav__item' + (isPrimaryNavActive(pathname, item) ? ' is-active' : '')}
                onClick={() => navigateToItem(item)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    navigateToItem(item)
                  }
                }}
                role="button"
                tabIndex={0}
                aria-label={item.label}
                aria-current={isPrimaryNavActive(pathname, item) ? 'page' : undefined}
                data-testid={`nav-${item.id}`}
              >
                <AppIcon name={item.icon} />
                <span className="nav__label">{item.label}</span>
                {item.id === 'overview' && Number(alertCount || 0) > 0 && (
                  <span className="nav__badge" aria-label={`待处理告警 ${alertCount}`}>{alertCount}</span>
                )}
              </div>
            ))}
          </nav>
        </div>
      </aside>

      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        <header className="topbar">
          <div className="topbar__page" data-testid="topbar-page-title">{currentPage.label}</div>
          <div className="topbar__scope"><ScopeBar key={scopeVersion} /></div>
          <div className="topbar__spacer" />
          <Dropdown
            trigger={['click']}
            dropdownRender={() => (
              <div style={{ width: 340, background: 'var(--surface-1)', borderRadius: 12, boxShadow: 'var(--shadow-lg)', border: '1px solid var(--border)', overflow: 'hidden' }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 14px', borderBottom: '1px solid var(--border-soft)' }}>
                  <span style={{ fontWeight: 600, fontSize: 13 }}>未解决告警</span>
                  <span onClick={() => navigate('/overview?tab=alerts')} style={{ fontSize: 12, color: 'var(--primary)', cursor: 'pointer' }}>在总览查看 →</span>
                </div>
                <div style={{ maxHeight: 320, overflowY: 'auto' }}>
                  {(recentAlerts || []).length === 0 ? (
                    <div style={{ padding: 20, textAlign: 'center', color: 'var(--text-muted)', fontSize: 12 }}>当前集群没有未解决告警</div>
                  ) : (
                    (recentAlerts || []).slice(0, 6).map((a: any) => (
                      <div
                        key={a.id || a.alert_id || a.title}
                        onClick={() => navigate('/overview?tab=alerts')}
                        data-testid="notification-alert-item"
                        style={{ display: 'flex', gap: 10, padding: '10px 14px', borderBottom: '1px solid var(--border-soft)', cursor: 'pointer', alignItems: 'flex-start' }}
                      >
                        <span style={{ width: 8, height: 8, borderRadius: '50%', marginTop: 5, flexShrink: 0, background: a.severity === 'critical' ? 'var(--danger)' : a.severity === 'warning' ? 'var(--warning)' : 'var(--primary)' }} />
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontSize: 12, color: 'var(--text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.rule_name || a.title || a.summary || a.alert_name || '告警'}</div>
                          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>{(a.object || a.service_name || a.service || '')}{a.created_at ? ` · ${String(a.created_at).slice(5, 16).replace('T', ' ')}` : ''}</div>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            )}
          >
            <div className="topbar__icon-btn" title="未解决告警" aria-label="未解决告警">
              {Number(alertCount || 0) > 0 && <span className="ping" />}
              <AppIcon name="bell" />
            </div>
          </Dropdown>
          <Dropdown
            menu={{
              items: [
                { key: 'identity', label: `${userLabel}（统一账户）`, disabled: true },
                { type: 'divider' },
                { key: 'password', label: '修改密码', onClick: () => navigate('/change-password') },
                { key: 'settings', label: '设置', onClick: () => navigate('/settings') },
                { type: 'divider' },
                {
                  key: 'logout',
                  label: '退出登录',
                  danger: true,
                  onClick: () => {
                    logout()
                    navigate('/login')
                    window.location.reload()
                  },
                },
              ],
            }}
          >
            <div className="user-chip" style={{ cursor: 'pointer' }}>
              <div className="avatar">{displayName}</div>
              <span className="text-sm">{userLabel}</span>
            </div>
          </Dropdown>
        </header>

        <main style={{ flex: 1, padding: '20px 24px', overflow: 'auto', minHeight: 0 }} data-testid={`page-${currentPage.id}`}>
          {scopeError && !scopeLoading ? (
            <Alert showIcon type="error" message="作用域加载失败" description={scopeError} style={{ marginBottom: 16 }} />
          ) : null}
          <MigrationNotice />
          {narrow && currentPage.id !== 'overview' ? (
            <div className="production-width-gate" role="alert" data-testid="production-width-gate">
              当前窗口宽度不足 1024px，生产操作仅在只读模式下可用。请将窗口扩大后继续。
            </div>
          ) : (
            <Suspense fallback={<div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>}>
              <Routes>
                <Route path="/ai-operations" element={<AiOperations />} />
                <Route path="/overview" element={<Overview />} />
                <Route path="/clusters" element={<ClusterEntry />} />
                <Route path="/clusters/:clusterUid/overview" element={<ClusterRouteGate><ClusterOverview /></ClusterRouteGate>} />
                <Route path="/observability" element={<Observability />} />
                <Route path="/knowledge-graph" element={<KnowledgeGraph />} />
                <Route path="/knowledge" element={<Knowledge />} />
                <Route path="/reports" element={<Reports />} />
                <Route path="/settings" element={<Settings />} />
                {/* 根路径即默认首页：AI 智能运维（D-03） */}
                <Route path="/" element={<Navigate replace to={AI_OPERATIONS_PATH} />} />
                <Route path="*" element={<LegacyOrNotFound />} />
              </Routes>
            </Suspense>
          )}
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
