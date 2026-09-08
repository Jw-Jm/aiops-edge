import React, { useState, lazy, Suspense, useEffect } from 'react'
import { Routes, Route, useNavigate, useLocation } from 'react-router-dom'
import { Alert, Dropdown, Spin } from 'antd'
import { useUIStore } from './store/uiStore'
import { useScopeStore } from './store/scopeStore'
import { useAuthStore } from './store/authStore'
import AiDock from './components/AiDock'
import ClusterSwitcher from './components/ClusterSwitcher'
import AppIcon, { AppIconName } from './components/AppIcons'
import RequireAuth from './components/RequireAuth'
import { getAlertEvents } from './api/client'

// ===== 懒加载页面（全新 IA）=====
const Login = lazy(() => import('./pages/Login'))
const ChangePassword = lazy(() => import('./pages/ChangePassword'))
const Overview = lazy(() => import('./pages/Overview'))
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
const Admin = lazy(() => import('./pages/admin/AdminHome'))
const NotFound = lazy(() => import('./pages/NotFound'))

// ===== 侧栏导航：方案定义的 7 个一级入口 =====
interface NavItem { path: string; label: string; icon: AppIconName; badge?: string; adminOnly?: boolean }

const NAV_ITEMS: NavItem[] = [
  { path: '/overview', label: '工作台', icon: 'overview' },
  { path: '/investigation', label: '调查', icon: 'chat' },
  { path: '/resources', label: '资源', icon: 'assets' },
  { path: '/observe', label: '观测', icon: 'monitor', badge: 'dynamic' },
  { path: '/actions', label: '处置', icon: 'approvals' },
  { path: '/reports', label: '报告', icon: 'reports' },
  { path: '/admin', label: '系统管理', icon: 'settings', adminOnly: true },
]

function visibleNavItems(role: string): NavItem[] {
  return NAV_ITEMS.filter((item) => !item.adminOnly || role === 'admin')
}

function AppLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const collapsed = useUIStore((s) => s.collapsed)
  const toggleCollapsed = useUIStore((s) => s.toggleCollapsed)
  const initializeScope = useScopeStore((s) => s.initialize)
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const scopeLoading = useScopeStore((s) => s.loading)
  const scopeError = useScopeStore((s) => s.error)
  const logout = useAuthStore((s) => s.logout)
  const [clock, setClock] = useState('')
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

  // 高亮当前路由
  const pathname = location.pathname
  const auth = useAuthStore()
  const role = auth.role
  // PF-LOGIC-013: 侧栏按 role 过滤后的导航组
  const visibleNav = visibleNavItems(role)
  const selectedKey = visibleNav.find((it) => it.path === pathname)?.path
    || visibleNav.find((it) => pathname.startsWith(it.path + '/'))?.path
    || (pathname.startsWith('/observability/') || pathname.startsWith('/alerts/') || pathname.startsWith('/capacity') || pathname.startsWith('/infra/') || pathname.startsWith('/hardware') || pathname.startsWith('/changes') ? '/resources' : '/overview')
  const currentLabel = visibleNav.find((m) => m.path === selectedKey)?.label || ''

  useEffect(() => {
    const t = setInterval(() => {
      const d = new Date()
      setClock(`${d.toLocaleDateString('zh-CN')} ${d.toLocaleTimeString('zh-CN', { hour12: false })}`)
    }, 1000)
    return () => clearInterval(t)
  }, [])

  const userLabel = auth.displayName || auth.username || '用户'
  const displayName = userLabel.slice(0, 1).toUpperCase()

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--bg)' }}>
      {/* 侧栏 */}
      <aside className="sidebar" style={{ width: collapsed ? 64 : 232, flexShrink: 0, transition: 'width .2s' }}>
        <div className="brand" style={{ padding: collapsed ? '16px 12px' : undefined, justifyContent: collapsed ? 'center' : undefined }}>
          <div className="brand__logo">观</div>
          {!collapsed && (
            <div>
              <div className="brand__name">智能可观测平台</div>
              <div className="brand__sub">AIOps</div>
            </div>
          )}
        </div>

        {!collapsed && (
          <div style={{ padding: '6px 12px 2px' }}>
            <div className="nav__hero" onClick={() => navigate('/ai/chat')}>
              <span className="nh-ic"><AppIcon name="chat" /></span>
              <span className="nh-txt"><span className="nh-title">AI 运维助手</span><span className="nh-sub">自然语言指挥</span></span>
            </div>
          </div>
        )}

        <div className="sidebar__scroll">
          <nav className="nav">
            {visibleNav.map((it) => (
              <div key={it.path} className={'nav__item' + (selectedKey === it.path ? ' is-active' : '')}
                onClick={() => navigate(it.path)} title={collapsed ? it.label : undefined}>
                <AppIcon name={it.icon} />
                {!collapsed && <span>{it.label}</span>}
                {!collapsed && it.badge && (
                  <span className="nav__badge">
                    {it.badge === 'dynamic' ? (alertCount ?? '') : it.badge}
                  </span>
                )}
              </div>
            ))}
          </nav>
        </div>

        <div className="nav__collapse-btn" onClick={toggleCollapsed}>
          <AppIcon name="collapse" />
          {!collapsed && <span style={{ flex: 1 }}>收起菜单</span>}
        </div>
      </aside>

      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        {/* 顶栏 */}
        <header className="topbar">
          <ClusterSwitcher />
          <div className="topbar__spacer" />
          {currentLabel && <span className="topbar__label" style={{ fontSize: 13, color: 'var(--text-secondary)', fontWeight: 500 }}>{currentLabel}</span>}
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
          <span className="topbar__clock" style={{ fontSize: 12, color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums' }}>{clock}</span>
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
          {!activeClusterId && !scopeLoading ? (
            <Alert
              showIcon
              type={scopeError ? 'error' : 'warning'}
              message={scopeError ? '作用域加载失败' : '请选择作用域'}
              description={scopeError || '当前页面不会展示跨集群混合数据。请先在右上角选择一个服务端已授权的集群。'}
              style={{ marginBottom: 16 }}
            />
          ) : null}
          <Suspense fallback={<div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>}>
            <Routes>
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
              <Route path="/ai/chat" element={<AiChat />} />
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
          </Suspense>
        </main>
      </div>

      <AiDock />
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
