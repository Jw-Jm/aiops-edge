import React, { useEffect, useState } from 'react'
import { Alert, Button, Col, Row, Tag } from 'antd'
import { useNavigate } from 'react-router-dom'
import { getPlatformClusters, getPlatformOverview, type PlatformCluster, type PlatformOverview } from '../../api/platform'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import { Breadcrumb, Empty, PageHeader, PaneCard, StatusBadge } from '../../components/ui/PageKit'
import type { PlatformClusterHealth } from '../../api/platform'

const STATUS_LABELS: Record<PlatformClusterHealth, string> = { healthy: '健康', degraded: '降级', critical: '严重', unknown: '未知' }
const STATUS_TONES: Record<PlatformClusterHealth, 'ok' | 'warn' | 'crit' | 'muted'> = { healthy: 'ok', degraded: 'warn', critical: 'crit', unknown: 'muted' }
const SORT_ORDER: Record<PlatformClusterHealth, number> = { critical: 0, degraded: 1, unknown: 2, healthy: 3 }
const CARRIER_LABELS = ['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Pod', 'Kubernetes Service', 'Ingress']

function formatTime(value?: string): string {
  if (!value) return '暂无'
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString('zh-CN', { hour12: false }) : value
}

function FactStat({ label, value, hint, tone }: { label: string; value: React.ReactNode; hint?: string; tone?: 'ok' | 'warn' | 'crit' | 'muted' }) {
  return <div className="platform-fact-stat"><span>{label}</span><strong className={tone ? `platform-fact-stat__value platform-fact-stat__value--${tone}` : 'platform-fact-stat__value'}>{value}</strong>{hint && <small>{hint}</small>}</div>
}

function ClusterStateLine({ overview }: { overview: PlatformOverview }) {
  const states: PlatformClusterHealth[] = ['healthy', 'degraded', 'critical', 'unknown']
  return <div className="platform-cluster-state-line">{states.map((state) => <span key={state}><StatusBadge text={STATUS_LABELS[state]} tone={STATUS_TONES[state]} /><strong>{overview.clusterStates[state]}</strong></span>)}</div>
}

function ClusterList({ clusters, onOpen }: { clusters: PlatformCluster[]; onOpen: (clusterId: string) => void }) {
  const sorted = [...clusters].sort((a, b) => SORT_ORDER[a.status] - SORT_ORDER[b.status] || a.name.localeCompare(b.name, 'zh-CN'))
  if (!sorted.length) return <Empty text="暂无授权集群" hint="平台总览只展示当前用户有权查看的实际 Kubernetes 集群" />
  return <div className="platform-cluster-list">{sorted.map((cluster) => <button type="button" className="platform-cluster-row" key={cluster.clusterId} onClick={() => onOpen(cluster.clusterId)}><span className="platform-cluster-row__main"><StatusBadge text={STATUS_LABELS[cluster.status]} tone={STATUS_TONES[cluster.status]} /><strong>{cluster.name || cluster.clusterId}</strong><small>{cluster.clusterId}</small></span><span className="platform-cluster-row__time">最新 {formatTime(cluster.updatedAt)}</span><span aria-hidden>→</span></button>)}</div>
}

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const [overview, setOverview] = useState<PlatformOverview | null>(null)
  const [clusters, setClusters] = useState<PlatformCluster[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [error, setError] = useState<unknown>()

  const load = () => {
    const controller = new AbortController()
    setState('loading')
    setError(undefined)
    void Promise.all([getPlatformOverview(controller.signal), getPlatformClusters({}, controller.signal)]).then(([nextOverview, nextClusters]) => {
      setOverview(nextOverview)
      setClusters(nextClusters.clusters)
      setState(nextOverview.meta.partial ? 'partial' : nextOverview.meta.stale ? 'stale' : 'ready')
    }).catch((reason) => {
      setError(reason)
      setState(reason?.response?.status === 403 ? 'forbidden' : 'error')
    })
    return () => controller.abort()
  }

  useEffect(() => {
    const dispose = load()
    return dispose
  }, [])

  return <div className="platform-overview-page">
    <Breadcrumb items={[{ t: '平台总览' }, { t: '云平台运营态势' }]} />
    <PageHeader title="云平台运营态势" desc="全部纳管 Kubernetes 集群 · 平台总览不受当前集群选择影响" actions={<Button onClick={load}>刷新</Button>} />
    <BoundedDataRegion state={state} error={error} onRetry={load} meta={overview?.meta}>
      {overview && <>
        {overview.meta.warningCodes.length > 0 && <Alert type="warning" showIcon message="部分数据需要关注" description={overview.meta.warningCodes.join(' · ')} style={{ marginBottom: 16 }} />}
        <Row gutter={[12, 12]} className="platform-fact-grid">
          <Col xs={12} md={6}><FactStat label="当前活动严重问题" value={overview.activeCriticalIssues} hint="可下钻到实际资源" tone={overview.activeCriticalIssues > 0 ? 'crit' : 'ok'} /></Col>
          <Col xs={12} md={6}><FactStat label="受影响集群" value={overview.affectedClusters} hint={`纳管集群 ${overview.managedClusters}`} tone={overview.affectedClusters > 0 ? 'warn' : 'ok'} /></Col>
          <Col xs={12} md={6}><FactStat label="未知/陈旧集群" value={overview.unknownOrStaleClusters} hint="数据可信度缺口" tone={overview.unknownOrStaleClusters > 0 ? 'warn' : 'ok'} /></Col>
          <Col xs={12} md={6}><FactStat label="采集覆盖" value={`${overview.coverage.covered}/${overview.coverage.expected}`} hint={overview.coverage.ratio == null ? '覆盖率不可用' : `${(overview.coverage.ratio * 100).toFixed(1)}%`} tone={overview.coverage.ratio != null && overview.coverage.ratio >= 0.98 ? 'ok' : 'warn'} /></Col>
        </Row>
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col xs={24} lg={15}><PaneCard title="纳管集群分布" action={<Tag>共 {overview.managedClusters} 个</Tag>}><ClusterStateLine overview={overview} /><ClusterList clusters={clusters} onOpen={(id) => navigate(`/clusters/${encodeURIComponent(id)}`)} /></PaneCard></Col>
          <Col xs={24} lg={9}><PaneCard title="当前最高优先级问题">{overview.highestPriority ? <div className="platform-priority"><StatusBadge text="严重" tone="crit" /><h3>{overview.highestPriority.title}</h3><p>{overview.highestPriority.clusterId} · {overview.highestPriority.resourceUid}</p><small>最近观察 {formatTime(overview.highestPriority.observedAt)}</small><Button type="primary" style={{ marginTop: 16 }} onClick={() => navigate(`/clusters/${encodeURIComponent(overview.highestPriority!.clusterId)}/investigations`)}>进入调查</Button></div> : <Empty text="当前没有最高优先级问题" />}</PaneCard></Col>
        </Row>
        <PaneCard title="平台关注的资源载体" style={{ marginTop: 16 }}><div className="platform-carrier-list">{CARRIER_LABELS.map((label) => <Tag key={label}>{label}</Tag>)}</div><p className="platform-carrier-list__hint">进入实际集群后查看各资源类型的数量、异常和数据质量。</p></PaneCard>
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col xs={24} lg={15}><PaneCard title="集群健康状态依据"><div className="platform-timeline-facts"><FactStat label="最新数据" value={formatTime(overview.freshestAt)} /><FactStat label="最旧有效数据" value={formatTime(overview.oldestValidAt)} /><FactStat label="四态分布" value={<ClusterStateLine overview={overview} />} /></div></PaneCard></Col>
          <Col xs={24} lg={9}><PaneCard title="运维数据与能力状态"><div className="platform-capability"><strong>{overview.capabilitySummary.total > 0 ? `${overview.capabilitySummary.healthy}/${overview.capabilitySummary.total}` : '不可用'}</strong><span>已读取的能力状态</span>{overview.capabilitySummary.issues.map((issue) => <Tag color="warning" key={issue}>{issue}</Tag>)}</div></PaneCard></Col>
        </Row>
      </>}
    </BoundedDataRegion>
  </div>
}

export default Overview
