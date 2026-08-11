import React, { useState, lazy, Suspense, useEffect } from 'react'
import { Routes, Route, useNavigate, useLocation } from 'react-router-dom'
import { Dropdown, Spin } from 'antd'
import { useUIStore } from './store/uiStore'
import { useAuthStore } from './store/authStore'
import CommandPalette from './components/CommandPalette'
import AiDock from './components/AiDock'
import ClusterSwitcher from './components/ClusterSwitcher'
import AppIcon, { AppIconName } from './components/AppIcons'
import RequireAuth from './components/RequireAuth'
import { getAlertEvents } from './api/client'

// ===== 懒加载页面（全新 IA）=====
const Login = lazy(() => import('./pages/Login'))
const Overview = lazy(() => import('./pages/Overview'))
const ServiceObservability = lazy(() => import('./pages/observability/ServiceObservability'))
const Trace = lazy(() => import('./pages/observability/Trace'))
const LogMetrics = lazy(() => import('./pages/observability/LogMetrics'))
const AlertEvents = lazy(() => import('./pages/alerts/AlertEvents'))
const AlertRules = lazy(() => import('./pages/alerts/AlertRules'))
const AiChat = lazy(() => import('./pages/ai/AiChat'))
const AiTasks = lazy(() => import('./pages/ai/AiTasks'))
const AiWorkflow = lazy(() => import('./pages/ai/AiWorkflow'))
const AiTools = lazy(() => import('./pages/ai/AiTools'))
const Capacity = lazy(() => import('./pages/capacity/Capacity'))
const Report = lazy(() => import('./pages/report/Report'))
const AdminUsers = lazy(() => import('./pages/admin/AdminUsers'))
const AdminSettings = lazy(() => import('./pages/admin/AdminSettings'))

// ===== 侧栏导航：7 大板块 =====
interface NavItem { path: string; label: string; icon: AppIconName; badge?: string }
interface NavGroup { title: string; collapsed?: boolean; footer?: boolean; items: NavItem[] }

const NAV_GROUPS: NavGroup[] = [
  {
    title: '总览',
    items: [{ path: '/overview', label: '工作台首页', icon: 'overview' }],
  },
  {
    title: '可观测',
    items: [
      { path: '/observability/service', label: '服务全景', icon: 'topology' },
      { path: '/observability/trace', label: '链路追踪', icon: 'traces' },
      { path: '/observability/log', label: '日志与指标', icon: 'logs' },
    ],
  },
  {
    title: '告警',
    items: [
      { path: '/alerts/events', label: '告警事件', icon: 'alerts', badge: 'dynamic' },
      { path: '/alerts/rules', label: '告警规则', icon: 'settings' },
    ],
  },
  {
    title: '智能运维',
    items: [
      { path: '/ai/chat', label: 'AI 对话', icon: 'chat' },
      { path: '/ai/tasks', label: '任务工作台', icon: 'tasks' },
      { path: '/ai/workflow', label: '工作流', icon: 'workflow' },
      { path: '/ai/tools', label: 'AI 工具', icon: 'nl2sql' },
    ],
  },
  {
    title: '容量与资源',
    items: [
      { path: '/capacity', label: '容量预测', icon: 'capacity' },
    ],
  },
  {
    title: '报告',
    items: [
      { path: '/report', label: '报告中心', icon: 'reports' },
    ],
  },
  {
    title: '系统管理',
    footer: true,
    items: [
      { path: '/admin/users', label: '用户管理', icon: 'users' },
      { path: '/admin/settings', label: '系统设置', icon: 'settings' },
    ],
  },
]

const allNav = NAV_GROUPS.flatMap((g) => g.items)

function AppLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const collapsed = useUIStore((s) => s.collapsed)
  const toggleCollapsed = useUIStore((s) => s.toggleCollapsed)
  const setCommandOpen = useUIStore((s) => s.setCommandOpen)
  const refreshClusters = useUIStore((s) => s.refreshClusters)
  const logout = useAuthStore((s) => s.logout)
  const [clock, setClock] = useState('')
  const [navCollapsed, setNavCollapsed] = useState<Record<string, boolean>>({})
  const [alertCount, setAlertCount] = useState<number | null>(null)

  // 初始化拉取集群列表（多集群纳管入口）
  useEffect(() => {
    refreshClusters()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // P3-1: 侧栏告警 badge 动态拉取真实告警数（替代硬编码 12）
  useEffect(() => {
    const loadAlerts = () => {
      getAlertEvents({ limit: 1 }).then((r) => {
        const d = r.data
        // API 返回 {count: 本次条数, total: 总告警数}；优先用 total 反映真实告警总量
        const n = Array.isArray(d) ? d.length : (d?.total ?? d?.count ?? 0)
        setAlertCount(Number(n) || 0)
      }).catch(() => setAlertCount(null))
    }
    loadAlerts()
    const t = setInterval(loadAlerts, 30000) // 30s 刷新
    return () => clearInterval(t)
  }, [])

  // 高亮当前路由
  const pathname = location.pathname
  const selectedKey = allNav.find((it) => it.path === pathname)?.path
    || allNav.find((it) => pathname.startsWith(it.path + '/'))?.path
    || '/overview'
  const currentLabel = allNav.find((m) => m.path === selectedKey)?.label || ''

  useEffect(() => {
    const t = setInterval(() => {
      const d = new Date()
      setClock(`${d.toLocaleDateString('zh-CN')} ${d.toLocaleTimeString('zh-CN', { hour12: false })}`)
    }, 1000)
    return () => clearInterval(t)
  }, [])

  const displayName = (localStorage.getItem('display_name') || localStorage.getItem('username') || 'admin').slice(0, 1).toUpperCase()

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
            {NAV_GROUPS.filter((g) => !g.footer).map((g) => {
              const isCollapsed = navCollapsed[g.title]
              return (
                <div key={g.title} className={'nav__group' + (isCollapsed ? ' is-collapsed' : '')}>
                  <div className="nav__group-label" onClick={() => setNavCollapsed((s) => ({ ...s, [g.title]: !s[g.title] }))}>
                    <span>{g.title}</span>
                    <span className="chev"><AppIcon name="chevron" size={12} /></span>
                  </div>
                  <div className="nav__group-items">
                    {g.items.map((it) => (
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
                  </div>
                </div>
              )
            })}
          </nav>
        </div>

        <div className="nav__footer">
          {NAV_GROUPS.filter((g) => g.footer).map((g) => (
            <div key={g.title}>
              {!collapsed && <div className="nav__group-label">{g.title}</div>}
              {g.items.map((it) => (
                <div key={it.path} className={'nav__item' + (selectedKey === it.path ? ' is-active' : '')}
                  onClick={() => navigate(it.path)} title={collapsed ? it.label : undefined}>
                  <AppIcon name={it.icon} />
                  {!collapsed && <span>{it.label}</span>}
                </div>
              ))}
            </div>
          ))}
        </div>

        <div className="nav__collapse-btn" onClick={toggleCollapsed}>
          <AppIcon name="collapse" />
          {!collapsed && <span style={{ flex: 1 }}>收起菜单</span>}
        </div>
      </aside>

      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        {/* 顶栏 */}
        <header className="topbar">
          <div className="search-trigger" onClick={() => setCommandOpen(true)}>
            <AppIcon name="search" /><span>搜索页面、资源、告警…</span><span className="kbd">⌘ K</span>
          </div>
          <ClusterSwitcher />
          <div className="topbar__spacer" />
          {currentLabel && <span style={{ fontSize: 13, color: 'var(--text-secondary)', fontWeight: 500 }}>{currentLabel}</span>}
          <div className="env-switch"><span className="dot" />演示环境</div>
          <div className="topbar__icon-btn" title="通知" onClick={() => navigate('/alerts/events')}><span className="ping" /><AppIcon name="bell" /></div>
          <span style={{ fontSize: 12, color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums' }}>{clock}</span>
          <Dropdown
            menu={{
              items: [
                { key: 'settings', label: '系统设置', onClick: () => navigate('/admin/settings') },
                { type: 'divider' },
                { key: 'logout', label: '退出登录', danger: true, onClick: () => { logout(); navigate('/login'); window.location.reload() } },
              ],
            }}
          >
            <div className="user-chip" style={{ cursor: 'pointer' }}>
              <div className="avatar">{displayName}</div>
              <span className="text-sm">{localStorage.getItem('display_name') || localStorage.getItem('username') || 'admin'}</span>
            </div>
          </Dropdown>
        </header>

        {/* 内容区 */}
        <main style={{ flex: 1, padding: '20px 24px', overflow: 'auto', minHeight: 0 }}>
          <Suspense fallback={<div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>}>
            <Routes>
              <Route path="/overview" element={<Overview />} />
              <Route path="/observability/service" element={<ServiceObservability />} />
              <Route path="/observability/trace" element={<Trace />} />
              <Route path="/observability/log" element={<LogMetrics />} />
              <Route path="/alerts/events" element={<AlertEvents />} />
              <Route path="/alerts/rules" element={<AlertRules />} />
              <Route path="/ai/chat" element={<AiChat />} />
              <Route path="/ai/tasks" element={<AiTasks />} />
              <Route path="/ai/workflow" element={<AiWorkflow />} />
              <Route path="/ai/tools" element={<AiTools />} />
              <Route path="/capacity" element={<Capacity />} />
              <Route path="/report" element={<Report />} />
              <Route path="/admin/users" element={<AdminUsers />} />
              <Route path="/admin/settings" element={<AdminSettings />} />
              <Route path="/" element={<Overview />} />
              <Route path="*" element={<Overview />} />
            </Routes>
          </Suspense>
        </main>
      </div>

      <CommandPalette />
      <AiDock />
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Suspense fallback={<Spin />}><Login /></Suspense>} />
      <Route path="*" element={<RequireAuth><AppLayout /></RequireAuth>} />
    </Routes>
  )
}
