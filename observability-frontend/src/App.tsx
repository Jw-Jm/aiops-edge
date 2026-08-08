import React, { useState, useEffect } from 'react'
import { Routes, Route, useNavigate, useLocation } from 'react-router-dom'
import { Layout, Menu, ConfigProvider, theme, Space, Input, Tag, Avatar, Dropdown, Tooltip } from 'antd'
import { useUIStore } from './store/uiStore'
import CommandPalette from './components/CommandPalette'
import AgentSidePanel from './components/AgentSidePanel'
import {
  RobotOutlined, AlertOutlined, SettingOutlined,
  RadarChartOutlined, FileSearchOutlined, ToolOutlined,
  ApartmentOutlined, DatabaseOutlined, NodeIndexOutlined, CloudServerOutlined,
  BulbOutlined, SearchOutlined, BellOutlined, DownOutlined, ThunderboltOutlined,
  DashboardOutlined, DeploymentUnitOutlined, AuditOutlined, SafetyCertificateOutlined,
  BookOutlined, ThunderboltFilled, TeamOutlined, ClusterOutlined, DesktopOutlined, HddOutlined,
} from '@ant-design/icons'
import AIChat from './pages/AIChat'
import ChatThread from './pages/AIChat/ChatThread'
import Alerts from './pages/Alerts'
import IncidentDetail from './pages/Alerts/IncidentDetail'
import Skills from './pages/Skills'
import Agents from './pages/Agents'
import Workflows from './pages/Workflows'
import WorkflowEditor from './pages/Workflows/Editor'
import Settings from './pages/Settings'
import DeepFlow from './pages/DeepFlow'
import Login from './pages/Login'
import Logs from './pages/Logs'
import Tasks from './pages/Tasks'
import Services from './pages/Services'
import ServiceDetail from './pages/ServiceDetail'
import Topology from './pages/Topology'
import TopologyCatalog from './pages/TopologyCatalog'
import Traces from './pages/Traces'
import TraceDetail from './pages/TraceDetail'
import Overview from './pages/Overview'
import Monitor from './pages/Monitor'
import Approvals from './pages/Approvals'
import Audit from './pages/Audit'
import Knowledge from './pages/Knowledge'
import Rules from './pages/Rules'
import NL2SQL from './pages/NL2SQL'
import Users from './pages/Users'
import Shell from './pages/Shell'
import Reports from './pages/Reports'
import Catalog from './pages/Catalog'
import Devices from './pages/Devices'
import Clusters from './pages/Clusters'
import Snmp from './pages/SNMP'
import Hardware from './pages/Hardware'

const { Sider, Content, Header } = Layout

// 菜单：8 区段布局
const menuGroups = [
  {
    title: '总览',
    items: [
      { key: '/', icon: <DashboardOutlined />, label: '平台总览' },
    ],
  },
  {
    title: '可观测',
    items: [
      { key: '/services', icon: <DatabaseOutlined />, label: '服务列表' },
      { key: '/topology', icon: <ApartmentOutlined />, label: '服务拓扑' },
      { key: '/topology/catalog', icon: <ApartmentOutlined />, label: '拓扑目录' },
      { key: '/traces', icon: <NodeIndexOutlined />, label: '链路追踪' },
      { key: '/logs', icon: <FileSearchOutlined />, label: '日志查询' },
    ],
  },
  {
    title: '监控',
    items: [
      { key: '/monitor', icon: <RadarChartOutlined />, label: '监控面板' },
    ],
  },
  {
    title: '智能运维',
    items: [
      { key: '/aichat', icon: <RobotOutlined />, label: 'AI 诊断' },
      { key: '/skills', icon: <ToolOutlined />, label: '技能目录' },
      { key: '/agents', icon: <RobotOutlined />, label: 'AI 助理' },
      { key: '/workflows', icon: <DeploymentUnitOutlined />, label: '工作流' },
      { key: '/alerts', icon: <AlertOutlined />, label: '告警中心' },
      { key: '/approvals', icon: <SafetyCertificateOutlined />, label: '审批中心' },
      { key: '/audit', icon: <AuditOutlined />, label: '审计日志' },
      { key: '/nl2sql', icon: <ThunderboltFilled />, label: 'SQL 查询' },
    ],
  },
  {
    title: '任务',
    items: [
      { key: '/tasks', icon: <ToolOutlined />, label: '任务工作台' },
    ],
  },
  {
    title: '智能资产',
    items: [
      { key: '/knowledge', icon: <BookOutlined />, label: '知识库' },
      { key: '/rules', icon: <SettingOutlined />, label: '规则管理' },
    ],
  },
  {
    title: '基础设施',
    items: [
      { key: '/catalog', icon: <ClusterOutlined />, label: '服务目录' },
      { key: '/devices', icon: <DesktopOutlined />, label: '设备管理' },
      { key: '/clusters', icon: <CloudServerOutlined />, label: '集群管理' },
      { key: '/snmp', icon: <ClusterOutlined />, label: 'SNMP 网络设备' },
      { key: '/hardware', icon: <HddOutlined />, label: '硬件健康' },
    ],
  },
  {
    title: '运维工具',
    items: [
      { key: '/shell', icon: <CloudServerOutlined />, label: 'WebShell' },
      { key: '/reports', icon: <FileSearchOutlined />, label: '报告中心' },
    ],
  },
  {
    title: '设置',
    items: [
      { key: '/settings', icon: <SettingOutlined />, label: '系统设置' },
      { key: '/users', icon: <TeamOutlined />, label: '用户管理' },
    ],
  },
]

const allMenuItems = menuGroups.flatMap(g => g.items)

const AppLayout: React.FC = () => {
  const navigate = useNavigate()
  const location = useLocation()
  const collapsed = useUIStore((s) => s.collapsed)
  const toggleCollapsed = useUIStore((s) => s.toggleCollapsed)
  const darkMode = useUIStore((s) => s.darkMode)
  const setDarkMode = useUIStore((s) => s.setDarkMode)
  const [clock, setClock] = useState('')
  const seg = location.pathname.split('/')[1]
  const selectedKey = seg ? '/' + seg : '/'
  const currentLabel = allMenuItems.find(m => m.key === selectedKey)?.label || 'AIOps 智能运维平台'

  useEffect(() => {
    const token = localStorage.getItem('token')
    if (!token) navigate('/login', { replace: true })
  }, [navigate])

  // 时钟
  useEffect(() => {
    const tick = () => {
      const d = new Date()
      const p = (n: number) => String(n).padStart(2, '0')
      setClock(`${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`)
    }
    tick()
    const t = setInterval(tick, 1000)
    return () => clearInterval(t)
  }, [])

  const toggleDark = () => {
    setDarkMode(!darkMode)
  }

  // 深色主题 token（zinc 语义色板）
  const darkToken = {
    colorPrimary: '#1677ff',
    borderRadius: 8,
    colorBgLayout: '#09090b',
    colorBgContainer: '#18181b',
    colorBgElevated: '#27272a',
    colorText: '#f4f4f5',
    colorTextSecondary: '#a1a1aa',
    colorBorder: 'rgba(255,255,255,0.12)',
    colorBorderSecondary: 'rgba(255,255,255,0.08)',
    colorSplit: 'rgba(255,255,255,0.08)',
  }

  return (
    <ConfigProvider theme={{ algorithm: darkMode ? theme.darkAlgorithm : theme.defaultAlgorithm, token: darkMode ? darkToken : { colorPrimary: '#1677ff', borderRadius: 8 } }}>
      <Layout style={{ minHeight: '100vh' }}>
        {/* 侧边栏 */}
        <Sider collapsible collapsed={collapsed} onCollapse={toggleCollapsed} width={230} theme="dark"
          style={{ background: 'linear-gradient(180deg, #0d1526 0%, #0a0f1c 100%)', borderRight: '1px solid rgba(255,255,255,0.06)' }}>
          {/* Logo */}
          <div style={{ height: 64, display: 'flex', alignItems: 'center', padding: collapsed ? '0 12px' : '0 20px', gap: 10, borderBottom: '1px solid rgba(255,255,255,0.08)' }}>
            <div style={{ width: 34, height: 34, flexShrink: 0, background: 'linear-gradient(135deg, #1677ff, #722ed1)', borderRadius: 9, display: 'flex', alignItems: 'center', justifyContent: 'center', boxShadow: '0 4px 12px rgba(22,119,255,0.4)' }}>
              <ThunderboltOutlined style={{ color: '#fff', fontSize: 18 }} />
            </div>
            {!collapsed && (
              <div>
                <div style={{ color: '#fff', fontSize: 15, fontWeight: 700, letterSpacing: 0.5, lineHeight: 1.2 }}>AIOps</div>
                <div style={{ color: 'rgba(255,255,255,0.45)', fontSize: 11 }}>智能可观测平台</div>
              </div>
            )}
          </div>

          {/* 分组菜单 */}
          <Menu theme="dark" mode="inline" selectedKeys={[selectedKey]} onClick={({ key }) => navigate(key)}
            style={{ background: 'transparent', borderRight: 0, marginTop: 8 }}
            items={menuGroups.map(g => ({
              key: g.title,
              type: 'group',
              label: collapsed ? null : <span style={{ fontSize: 11, color: 'rgba(255,255,255,0.35)', letterSpacing: 1, paddingLeft: 4 }}>{g.title}</span>,
              children: g.items.map(it => ({ key: it.key, icon: it.icon, label: it.label })),
            }))}
          />
        </Sider>

        <Layout style={{ background: darkMode ? '#0a0f1c' : '#f0f2f5' }}>
          {/* 顶部 */}
          <Header style={{ background: darkMode ? '#0d1526' : '#fff', padding: '0 24px', borderBottom: '1px solid rgba(255,255,255,0.08)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', height: 56, lineHeight: '56px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
              <span style={{ fontSize: 16, fontWeight: 600, color: darkMode ? 'rgba(255,255,255,0.92)' : '#000' }}>{currentLabel}</span>
              <Tag color="blue" style={{ marginLeft: 4 }}>生产环境</Tag>
            </div>
            <Space size={16} align="center">
              <Input
                prefix={<SearchOutlined style={{ color: 'rgba(255,255,255,0.4)' }} />}
                placeholder="全局搜索服务 / 日志 / 告警..."
                style={{ width: 260, background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 6 }}
                onPressEnter={(e: any) => { const v = e.target.value.trim(); if (v) navigate(`/logs?q=${encodeURIComponent(v)}`) }}
              />
              <Tooltip title="告警中心">
                <BellOutlined style={{ fontSize: 16, color: darkMode ? 'rgba(255,255,255,0.7)' : '#666', cursor: 'pointer' }} onClick={() => navigate('/alerts')} />
              </Tooltip>
              <span style={{ fontSize: 12, color: darkMode ? 'rgba(255,255,255,0.5)' : '#999', fontVariantNumeric: 'tabular-nums' }}>{clock}</span>
              <Tooltip title={darkMode ? '切换浅色' : '切换深色'}>
                <BulbOutlined style={{ fontSize: 16, color: darkMode ? '#faad14' : '#666', cursor: 'pointer' }} onClick={toggleDark} />
              </Tooltip>
              <Dropdown menu={{ items: [{ key: 'settings', label: '系统设置', onClick: () => navigate('/settings') }] }}>
                <Space style={{ cursor: 'pointer' }}>
                  <Avatar size={28} style={{ background: 'linear-gradient(135deg, #1677ff, #722ed1)' }}>A</Avatar>
                  <DownOutlined style={{ fontSize: 10, color: 'rgba(255,255,255,0.4)' }} />
                </Space>
              </Dropdown>
            </Space>
          </Header>

          {/* 内容 */}
          <Content style={{ margin: 16, minHeight: 'calc(100vh - 88px)' }}>
            <div style={{ background: darkMode ? '#121826' : '#fff', padding: 20, borderRadius: 12, border: '1px solid rgba(255,255,255,0.06)', boxShadow: '0 8px 24px rgba(0,0,0,0.2)', minHeight: '100%' }}>
              <Routes>
                <Route path="/" element={<Overview />} />
                <Route path="/aichat" element={<AIChat />} />
                <Route path="/chat/:sessionId" element={<ChatThread />} />
                <Route path="/skills" element={<Skills />} />
                <Route path="/agents" element={<Agents />} />
                <Route path="/workflows" element={<Workflows />} />
                <Route path="/workflows/editor" element={<WorkflowEditor />} />
                <Route path="/services" element={<Services />} />
                <Route path="/services/:name" element={<ServiceDetail />} />
                <Route path="/topology" element={<Topology />} />
                <Route path="/topology/catalog" element={<TopologyCatalog />} />
                <Route path="/traces" element={<Traces />} />
                <Route path="/traces/:traceId" element={<TraceDetail />} />
                <Route path="/logs" element={<Logs />} />
                <Route path="/monitor" element={<Monitor />} />
                <Route path="/deepflow" element={<DeepFlow />} />
                <Route path="/alerts" element={<Alerts />} />
                <Route path="/alerts/incidents/:id" element={<IncidentDetail />} />
                <Route path="/tasks" element={<Tasks />} />
                <Route path="/approvals" element={<Approvals />} />
                <Route path="/audit" element={<Audit />} />
                <Route path="/knowledge" element={<Knowledge />} />
                <Route path="/rules" element={<Rules />} />
                <Route path="/nl2sql" element={<NL2SQL />} />
                <Route path="/shell" element={<Shell />} />
                <Route path="/reports" element={<Reports />} />
                <Route path="/users" element={<Users />} />
                <Route path="/catalog" element={<Catalog />} />
                <Route path="/devices" element={<Devices />} />
                <Route path="/clusters" element={<Clusters />} />
                <Route path="/snmp" element={<Snmp />} />
                <Route path="/hardware" element={<Hardware />} />
                <Route path="/settings" element={<Settings />} />
              </Routes>
            </div>
          </Content>
        </Layout>
      </Layout>
      <CommandPalette />
      <AgentSidePanel />
    </ConfigProvider>
  )
}

const App: React.FC = () => (
  <Routes>
    <Route path="/login" element={<Login />} />
    <Route path="/*" element={<AppLayout />} />
  </Routes>
)

export default App
