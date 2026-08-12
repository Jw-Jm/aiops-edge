import React, { useEffect, useState } from 'react'
import { Row, Col, Button, Space, Input, Tag } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getDashboardStats, DashboardStats, listApprovalTasks } from '../../api/client'
import { PageHeader, StatCard, StatusBadge, PaneCard, Empty, Breadcrumb } from '../../components/ui/PageKit'
import AppIcon from '../../components/AppIcons'
import { useUIStore } from '../../store/uiStore'

function sparkPts(arr: number[], w = 120, h = 40): string {
  if (!arr || arr.length < 2) return ''
  const max = Math.max(...arr), min = Math.min(...arr)
  const range = max - min || 1
  return arr.map((v, i) => `${((i / (arr.length - 1)) * w).toFixed(1)},${(h - ((v - min) / range) * (h - 4) - 2).toFixed(1)}`).join(' ')
}

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState<number>(0)
  const [aiQ, setAiQ] = useState(searchParams.get('q') || '')
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)
  // 当前集群显示名：all → 全部集群；否则按集群 name（主集群 default 映射回其 name）
  const curClusterName =
    currentClusterId === 'all'
      ? '全部集群'
      : (() => {
          const c = clusters.find((x) => (x.id === 1 ? 'default' : x.name) === currentClusterId)
          return c ? c.name : currentClusterId
        })()

  useEffect(() => {
    Promise.all([getDashboardStats(), listApprovalTasks({ limit: 1 })])
      .then(([s, t]) => {
        setStats(s.data)
        const tasks = (t.data as any)?.tasks || []
        setPending(tasks.filter((x: any) => x.status === 'waiting').length)
      })
      .finally(() => setLoading(false))
  }, [])

  const a = stats?.alerts
  // 系统健康度 = 100 扣除错误率与活跃告警严重度（critical 每个 -30，warning 每个 -10，info -3），
  // 避免"严重告警 3 个但健康度 100/100"的矛盾感知。
  const healthScore = stats
    ? Math.max(
        0,
        Math.min(100, Math.round(100 - (stats.error_rate ?? 0) - (a?.critical ?? 0) * 30 - (a?.warning ?? 0) * 10 - (a?.info ?? 0) * 3)),
      )
    : null
  const sparkCalls = sparkPts((stats?.trend || []).map((t) => t.calls))
  const sparkErrors = sparkPts((stats?.trend || []).map((t) => t.errors))

  return (
    <div>
      <Breadcrumb items={[{ t: '总览' }, { t: '工作台首页' }]} />
      <PageHeader title="工作台首页" desc="平台运行态势一览 · 聚焦关键风险与待办" />

      {/* 态势横幅 */}
      <div style={{ display: 'flex', gap: 16, marginBottom: 16, flexWrap: 'wrap' }}>
        <div className="card" style={{ flex: 2, minWidth: 320, marginBottom: 0, padding: 20, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <div style={{ fontSize: 16, fontWeight: 700, color: 'var(--text)' }}>当前态势</div>
              <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 2 }}>近 24h 告警与系统健康</div>
            </div>
            <Tag color="blue" style={{ borderRadius: 999 }}>{curClusterName}</Tag>
          </div>
          <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap' }}>
            <div><StatusBadge text={`严重 ${a?.critical ?? 0}`} tone={(a?.critical ?? 0) > 0 ? 'crit' : 'muted'} /></div>
            <div><StatusBadge text={`警告 ${a?.warning ?? 0}`} tone={(a?.warning ?? 0) > 0 ? 'warn' : 'muted'} /></div>
            <div><StatusBadge text={`信息 ${a?.info ?? 0}`} tone="info" /></div>
          </div>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span style={{ fontSize: 13, color: 'var(--text-muted)' }}>系统健康度</span>
            <span style={{ fontSize: 34, fontWeight: 700, color: healthScore !== null && healthScore >= 90 ? 'var(--success)' : healthScore !== null && healthScore >= 75 ? 'var(--warning)' : 'var(--danger)' }}>
              {healthScore ?? '—'}
            </span>
            <span style={{ color: 'var(--text-muted)' }}>/ 100</span>
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>依据：100 − 错误率 − 告警严重度（critical×30 / warning×10 / info×3）</div>
        </div>
        <div className="card" style={{ flex: 1, minWidth: 300, marginBottom: 0, padding: 20, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
          <div style={{ fontSize: 13, color: 'var(--text-secondary)', marginBottom: 10 }}>待办</div>
          <div style={{ fontSize: 28, fontWeight: 700, color: pending > 0 ? 'var(--warning)' : 'var(--text-muted)' }}>{pending} 项待审批</div>

        </div>
      </div>

      {/* 集中 KPI */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}><StatCard label="服务数量" value={loading ? '…' : (stats?.services ?? 0)} unit="个" spark={sparkCalls} sparkColor="var(--primary)" /></Col>
        <Col xs={12} md={6}><StatCard label="调用总量" value={loading ? '…' : (stats?.total_calls ?? 0).toLocaleString()} unit="次" /></Col>
        <Col xs={12} md={6}><StatCard label="请求错误率" value={loading ? '…' : `${(stats?.error_rate ?? 0).toFixed(2)}`} unit="%" trend={`${(stats?.error_rate ?? 0).toFixed(2)}%`} trendDir={(stats?.error_rate ?? 0) > 0 ? 'up' : 'flat'} spark={sparkErrors} sparkColor="var(--danger)" /></Col>
        <Col xs={12} md={6}><StatCard label="P95 延迟" value={loading ? '…' : `${(stats?.latency_p95 ?? 0).toFixed(1)}`} unit="ms" /></Col>
      </Row>

      {/* AI 快问 + 告警分布 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={14}>
          <PaneCard title="AI 快问快答" action={<Button type="link" onClick={() => navigate('/ai/chat')}>进入 AI 对话 →</Button>}>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <Input
                value={aiQ}
                onChange={(e) => setAiQ(e.target.value)}
                onPressEnter={() => { if (aiQ.trim()) navigate(`/ai/chat?q=${encodeURIComponent(aiQ)}`) }}
                placeholder="用自然语言提问，例如：分析 prod 集群故障根因"
                style={{ flex: 1, minWidth: 240 }}
                suffix={<span style={{ cursor: 'pointer', display: 'flex' }} onClick={() => { if (aiQ.trim()) navigate(`/ai/chat?q=${encodeURIComponent(aiQ)}`) }}><AppIcon name="send" /></span>}
              />
              {/* P2-16: "分析根因"基于当前输入（若已输入），否则用默认预设 */}
              <Button onClick={() => navigate(`/ai/chat?q=${encodeURIComponent(aiQ.trim() || '分析 prod 集群故障根因')}`)}>分析根因</Button>
              <Button onClick={() => navigate('/ai/chat?q=巡检所有 K8s 集群')}>集群巡检</Button>
            </div>
          </PaneCard>
        </Col>
        <Col xs={24} lg={10}>
          <PaneCard title="告警按服务分布">
            {(a?.by_service || []).length === 0
              ? <Empty text="暂无告警分布数据" />
              : (a?.by_service || []).slice(0, 6).map((s) => (
                  <div key={s.service} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 0', borderBottom: '1px solid var(--border-soft)' }}>
                    <span style={{ flex: 1, fontSize: 13 }}>{s.service}</span>
                    <Space size={6}>
                      {s.critical > 0 && <StatusBadge text={`严重 ${s.critical}`} tone="crit" />}
                      {s.warning > 0 && <StatusBadge text={`警告 ${s.warning}`} tone="warn" />}
                    </Space>
                  </div>
                ))}
          </PaneCard>
        </Col>
      </Row>
    </div>
  )
}

export default Overview
