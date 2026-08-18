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
const VirtualMachines = lazy(() => import('./pages/observability/VirtualMachines'))
const AlertEvents = lazy(() => import('./pages/alerts/AlertEvents'))
const AlertRules = lazy(() => import('./pages/alerts/AlertRules'))
const AiChat = lazy(() => import('./pages/ai/AiChat'))
const AiTools = lazy(() => import('./pages/ai/AiTools'))
const Capacity = lazy(() => import('./pages/capacity/Capacity'))
const Report = lazy(() => import('./pages/report/Report'))
const AdminUsers = lazy(() => import('./pages/admin/AdminUsers'))
const Approvals = lazy(() => import('./pages/admin/Approvals'))
const SLO = lazy(() => import('./pages/slo/SLO'))
const Knowledge = lazy(() => import('./pages/ai/Knowledge'))
const AdminSettings = lazy(() => import('./pages/admin/AdminSettings'))
// ongrid 对齐新增页面
const Workflows = lazy(() => import('./pages/ai/Workflows'))
const WorkflowsEditor = lazy(() => import('./pages/ai/Workflows/Editor'))
const WorkflowsDetail = lazy(() => import('./pages/ai/Workflows/Detail'))
const K8sActions = lazy(() => import('./pages/infra/K8sActions'))
const Grafana = lazy(() => import('./pages/observability/Grafana'))
// P2-4 新增页面：硬件健康 / 变更时间线 / 图谱视图
const Hardware = lazy(() => import('./pages/infra/Hardware'))
const Changes = lazy(() => import('./pages/infra/Changes'))
const KnowledgeGraph = lazy(() => import('./pages/ai/KnowledgeGraph'))

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
      { path: '/observability/vms', label: '虚拟机', icon: 'desktop' },
      { path: '/observability/grafana', label: 'Grafana 面板', icon: 'gauge' },
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
      { path: '/kg', label: '图谱视图', icon: 'topology' },
      { path: '/ai/tools', label: 'AI 工具', icon: 'nl2sql' },
      { path: '/ai/workflows', label: '工作流', icon: 'workflow' },
      { path: '/slo', label: 'SLO 目标', icon: 'settings' },
      { path: '/knowledge', label: '知识库', icon: 'knowledge' },
    ],
  },
  {
    title: '容量与资源',
    items: [
      { path: '/capacity', label: '容量预测', icon: 'capacity' },
      { path: '/infra/k8s', label: 'K8s 运维', icon: 'k8s' },
      { path: '/hardware', label: '硬件健康', icon: 'ipmi' },
    ],
  },
  {
    title: '报告',
    items: [
      { path: '/report', label: '报告中心', icon: 'reports' },
      { path: '/changes', label: '变更时间线', icon: 'tasks' },
    ],
  },
  {
    title: '系统管理',
    footer: true,
    items: [
      { path: '/admin/approvals', label: '审批中心', icon: 'approvals' },
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
  // 修复 5.7：通知抽屉需要最近告警明细，与 alertCount 一并拉取
  const [recentAlerts, setRecentAlerts] = useState<any[]>([])

  // 初始化拉取集群列表（多集群纳管入口）
  useEffect(() => {
    refreshClusters()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // P3-1: 侧栏告警 badge 动态拉取真实告警数（替代硬编码 12）
  useEffect(() => {
    const loadAlerts = () => {
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
          {currentLabel && <span className="topbar__label" style={{ fontSize: 13, color: 'var(--text-secondary)', fontWeight: 500 }}>{currentLabel}</span>}
          <div className="env-switch"><span className="dot" />演示环境</div>
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
                { key: 'password', label: '修改密码', disabled: true },
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
              <Route path="/observability/vms" element={<VirtualMachines />} />
              <Route path="/alerts/events" element={<AlertEvents />} />
              <Route path="/alerts/rules" element={<AlertRules />} />
              <Route path="/ai/chat" element={<AiChat />} />

              <Route path="/ai/tools" element={<AiTools />} />
              <Route path="/ai/workflows" element={<Workflows />} />
              <Route path="/ai/workflows/editor" element={<WorkflowsEditor />} />
              <Route path="/ai/workflows/:id" element={<WorkflowsDetail />} />
              <Route path="/slo" element={<SLO />} />
              <Route path="/knowledge" element={<Knowledge />} />
              <Route path="/capacity" element={<Capacity />} />
              <Route path="/infra/k8s" element={<K8sActions />} />
              <Route path="/hardware" element={<Hardware />} />
              <Route path="/changes" element={<Changes />} />
              <Route path="/kg" element={<KnowledgeGraph />} />
              <Route path="/observability/grafana" element={<Grafana />} />
              <Route path="/report" element={<Report />} />
              <Route path="/admin/approvals" element={<Approvals />} />
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
