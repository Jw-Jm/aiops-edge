import React, { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Col, Row, Tag } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import { getClusterOverview, type ClusterOverview, type FoundationFact, type ResourceKindSummary } from '../../api/clusterOverview'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import { Breadcrumb, Empty, PageHeader, PaneCard, StatusBadge } from '../../components/ui/PageKit'
import type { PlatformClusterHealth } from '../../api/platform'

const KIND_LABELS: Record<ResourceKindSummary['kind'], string> = {
  deployment: 'Deployment',
  statefulset: 'StatefulSet',
  daemonset: 'DaemonSet',
  job: 'Job',
  cronjob: 'CronJob',
  pod: 'Pod',
  k8s_service: 'Kubernetes Service',
  ingress: 'Ingress',
}

const FOUNDATION_LABELS: Record<FoundationFact['kind'], string> = {
  control_plane: '控制面',
  nodes_hosts: '节点与物理机',
  network: '网络面',
  storage: '存储面',
  kubevirt: 'KubeVirt',
}

const STATUS_LABELS: Record<PlatformClusterHealth, string> = { healthy: '健康', degraded: '降级', critical: '严重', unknown: '未知' }
const STATUS_TONES: Record<PlatformClusterHealth, 'ok' | 'warn' | 'crit' | 'muted'> = { healthy: 'ok', degraded: 'warn', critical: 'crit', unknown: 'muted' }

function formatTime(value?: string): string {
  if (!value) return '暂无'
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString('zh-CN', { hour12: false }) : value
}

function ClusterHeader({ data, onBack, onInvestigate }: { data: ClusterOverview; onBack: () => void; onInvestigate: () => void }) {
  return (
    <>
      <Breadcrumb items={[{ t: '平台总览', href: '/overview' }, { t: data.name || data.clusterId }]} onClick={(href) => { if (href) onBack() }} />
      <PageHeader
        title="集群详细总览"
        desc={<span>当前集群：{data.name || data.clusterId} · {data.clusterId} · 版本 {data.version || '未知'} · 最近同步 {formatTime(data.lastSyncAt)}</span>}
        actions={<div className="page-actions"><Button onClick={onBack}>返回平台总览</Button><Button type="primary" onClick={onInvestigate}>进入调查</Button></div>}
      />
      <h2 className="cluster-overview-cluster-name">{data.name || data.clusterId}</h2>
      <div className="cluster-overview-statusline">
        <StatusBadge text={STATUS_LABELS[data.status]} tone={STATUS_TONES[data.status]} />
        {data.statusReasons.map((reason) => <span key={reason} className="cluster-overview-statusline__reason">{reason}</span>)}
        <span className="cluster-overview-statusline__meta">采集覆盖 {data.coverage.covered}/{data.coverage.expected}</span>
      </div>
    </>
  )
}

function ResourceKindGrid({ items }: { items: ResourceKindSummary[] }) {
  return (
    <div className="cluster-resource-kind-grid">
      {items.map((item) => (
        <div className="cluster-resource-kind" key={item.kind}>
          <div className="cluster-resource-kind__label">{KIND_LABELS[item.kind]}</div>
          <div className="cluster-resource-kind__value">{item.total}</div>
          <div className="cluster-resource-kind__meta">异常 {item.abnormal} · 未知/陈旧 {item.unknownOrStale}</div>
        </div>
      ))}
    </div>
  )
}

function FoundationBand({ items }: { items: FoundationFact[] }) {
  const ordered = ['control_plane', 'nodes_hosts', 'network', 'storage', 'kubevirt'] as FoundationFact['kind'][]
  const byKind = new Map(items.map((item) => [item.kind, item]))
  return (
    <div className="cluster-foundation-grid">
      {ordered.map((kind) => {
        const item = byKind.get(kind) ?? { kind, status: 'unknown' as const, reason: '暂无证据', affectedResourceCount: 0 }
        return <div className="cluster-foundation-fact" key={kind}><div className="cluster-foundation-fact__head"><span>{FOUNDATION_LABELS[kind]}</span><StatusBadge text={STATUS_LABELS[item.status]} tone={STATUS_TONES[item.status]} /></div><p>{item.reason}</p><small>影响资源 {item.affectedResourceCount}</small></div>
      })}
    </div>
  )
}

const ClusterOverview: React.FC = () => {
  const { clusterId = '' } = useParams<{ clusterId: string }>()
  const navigate = useNavigate()
  const [data, setData] = useState<ClusterOverview | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [error, setError] = useState<unknown>()

  const load = () => {
    if (!clusterId) { setState('empty'); return }
    const controller = new AbortController()
    setState('loading')
    setError(undefined)
    void getClusterOverview(clusterId, controller.signal).then((result) => {
      setData(result)
      setState(result.meta.partial ? 'partial' : result.meta.stale ? 'stale' : 'ready')
    }).catch((reason) => {
      if (reason?.response?.status === 403) setState('forbidden')
      else { setError(reason); setState('error') }
    })
    return () => controller.abort()
  }

  useEffect(() => load(), [clusterId])

  const issueSummary = useMemo(() => data?.issues.slice(0, 5) ?? [], [data])
  return (
    <div className="cluster-overview-page">
      {data ? <ClusterHeader data={data} onBack={() => navigate('/overview')} onInvestigate={() => navigate(`/clusters/${encodeURIComponent(clusterId)}/investigations`)} /> : <PageHeader title="集群详细总览" desc={clusterId ? `集群 ${clusterId}` : '未选择集群'} />}
      <BoundedDataRegion state={state} error={error} onRetry={load} meta={data?.meta}>
        {data && <>
          <Row gutter={[16, 16]}>
            <Col xs={24} lg={16}><PaneCard title="当前最重要问题" action={<Tag color={data.issues.length ? 'red' : 'green'}>{data.issues.length} 项</Tag>}>
              {issueSummary.length ? <div className="cluster-issue-list">{issueSummary.map((issue) => <div className="cluster-issue-row" key={`${issue.clusterId}:${issue.resourceUid}:${issue.ruleId}`}><StatusBadge text={issue.severity === 'critical' ? '严重' : issue.severity} tone={issue.severity === 'critical' ? 'crit' : 'warn'} /><strong>{issue.title}</strong><span>{issue.resourceUid}</span></div>)}</div> : <Empty text="当前集群暂无活跃严重问题" />}
            </PaneCard></Col>
            <Col xs={24} lg={8}><PaneCard title="数据质量"><div className="cluster-quality-summary"><strong>{data.coverage.covered}/{data.coverage.expected}</strong><span>采集覆盖</span><small>最新同步 {formatTime(data.lastSyncAt)}</small></div></PaneCard></Col>
          </Row>
          <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
            <Col xs={24} lg={12}><PaneCard title="容器资源" action={<Button type="link" onClick={() => navigate(`/clusters/${encodeURIComponent(clusterId)}/resources`)}>查看资源目录 →</Button>}><ResourceKindGrid items={data.resourceKinds} /></PaneCard></Col>
            <Col xs={24} lg={12}><PaneCard title="KubeVirt 虚拟机" action={<Button type="link" onClick={() => navigate(`/clusters/${encodeURIComponent(clusterId)}/resources?group=kubevirt`)}>查看虚拟机 →</Button>}><div className="kubevirt-summary-grid"><div><strong>{data.kubevirt.vm}</strong><span>VM</span></div><div><strong>{data.kubevirt.vmi}</strong><span>VMI</span></div><div><strong>{data.kubevirt.notReady}</strong><span>未就绪</span></div><div><strong>{data.kubevirt.migrating + data.kubevirt.failedMigration}</strong><span>迁移异常</span></div><div><strong>{data.kubevirt.storageAffected + data.kubevirt.networkAffected}</strong><span>基础能力影响</span></div></div></PaneCard></Col>
          </Row>
          <PaneCard title="集群基础能力" style={{ marginTop: 16 }}><FoundationBand items={data.foundation} /></PaneCard>
        </>}
      </BoundedDataRegion>
    </div>
  )
}

export default ClusterOverview
