import React, { useEffect, useState } from 'react'
import { Alert, Button, Table, Tag, Tooltip } from 'antd'
import { useNavigate } from 'react-router-dom'
import {
  getPlatformCapacity,
  getPlatformClusters,
  getPlatformOverview,
  type CapacityClusterFact,
  type PlatformCluster,
  type PlatformOverview,
} from '../../api/platform'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import { Empty, PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import type { PlatformClusterHealth } from '../../api/platform'
import { CLUSTER_HEALTH_LABELS, CLUSTER_HEALTH_SORT_ORDER, CLUSTER_HEALTH_TONES, capabilitySummaryText, registrationLabel } from '../../features/platform/healthPresentation'

const STATUS_LABELS = CLUSTER_HEALTH_LABELS
const STATUS_TONES = CLUSTER_HEALTH_TONES
const SORT_ORDER = CLUSTER_HEALTH_SORT_ORDER

const QUALITY_TONE: Record<string, StatusTone> = {
  healthy: 'ok',
  partial: 'warn',
  stale: 'warn',
  unknown: 'muted',
  not_connected: 'muted',
  failed: 'crit',
}

const QUALITY_LABEL: Record<string, string> = {
  healthy: '数据完整',
  partial: '部分数据',
  stale: '数据陈旧',
  unknown: '质量未知',
  not_connected: '未接入',
  failed: '采集失败',
}

function formatTime(value?: string): string {
  if (!value) return '未提供'
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' }) : value
}

function fmtRatio(ratio: number | null): string {
  return ratio === null || ratio === undefined ? '未提供' : `${(ratio * 100).toFixed(1)}%`
}

function fmtCores(v: number): string {
  return `${Number.isFinite(v) ? v.toFixed(2) : '未提供'} 核`
}

function fmtBytes(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return '未提供'
  const gib = v / (1024 * 1024 * 1024)
  return `${gib.toFixed(1)} GiB`
}

function FactStat({ label, value, hint, tone }: { label: string; value: React.ReactNode; hint?: string; tone?: 'ok' | 'warn' | 'crit' | 'muted' }) {
  return <div className="platform-fact-stat"><span>{label}</span><strong className={tone ? `platform-fact-stat__value platform-fact-stat__value--${tone}` : 'platform-fact-stat__value'}>{value}</strong>{hint && <small>{hint}</small>}</div>
}

/** 跨集群比较条：颜色不单独承载语义，始终带数值文本与单位 */
function UtilizationBar({ ratio, label }: { ratio: number | null; label: string }) {
  if (ratio === null) return <span className="muted-sm">{label} 未提供</span>
  const pct = Math.min(100, Math.max(0, ratio * 100))
  const tone = pct >= 85 ? 'var(--danger)' : pct >= 70 ? 'var(--warning)' : 'var(--primary)'
  return (
    <div className="util-bar" role="img" aria-label={`${label} ${pct.toFixed(1)}%`}>
      <div className="util-bar__track"><div className="util-bar__fill" style={{ width: `${pct}%`, background: tone }} /></div>
      <span className="util-bar__value">{pct.toFixed(1)}%</span>
    </div>
  )
}

function ClusterStateLine({ overview }: { overview: PlatformOverview }) {
  const states: PlatformClusterHealth[] = ['healthy', 'degraded', 'critical', 'unknown']
  return <div className="platform-cluster-state-line">{states.map((state) => <span key={state}><StatusBadge text={STATUS_LABELS[state]} tone={STATUS_TONES[state]} /><strong>{overview.clusterStates[state]}</strong></span>)}</div>
}

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const [overview, setOverview] = useState<PlatformOverview | null>(null)
  const [clusters, setClusters] = useState<PlatformCluster[]>([])
  const [capacity, setCapacity] = useState<CapacityClusterFact[]>([])
  const [capacityState, setCapacityState] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading')
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [error, setError] = useState<unknown>()

  const load = () => {
    const controller = new AbortController()
    setState('loading')
    setCapacityState('loading')
    setError(undefined)
    getPlatformCapacity(controller.signal)
      .then((res) => {
        setCapacity(res.clusters)
        setCapacityState(res.clusters.length === 0 ? 'empty' : 'ready')
      })
      .catch(() => {
        setCapacity([])
        setCapacityState('error')
      })
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

  const sortedClusters = [...clusters].sort(
    (a, b) => SORT_ORDER[a.status] - SORT_ORDER[b.status] || a.name.localeCompare(b.name, 'zh-CN'),
  )
  const capacityByCluster = new Map(capacity.map((c) => [c.clusterId, c]))

  return <div className="platform-overview-page">
    <PageHeader
      title="总览"
      desc="全部纳管集群 · 告警态势、集群口径 CPU/内存、容量风险与数据质量"
      actions={<Button onClick={load}>刷新</Button>}
    />
    <BoundedDataRegion state={state} error={error} onRetry={load} meta={overview?.meta}>
      {overview && <>
        {/* 至多一条数据可信度提示：受影响来源 + 影响能力 + 最近成功时间（§6.2 禁止重复 warning code） */}
        {(overview.meta.partial || overview.meta.stale) && (
          <Alert
            type="warning"
            showIcon
            data-testid="data-trust-banner"
            style={{ marginBottom: 16 }}
            message={overview.meta.partial ? '部分数据来源不可用' : '数据已超过新鲜度要求'}
            description={`受影响能力：集群容量、告警聚合与数据质量判定；最近成功时间 ${formatTime(overview.freshestAt)}。未接入的集群不会被显示为 0 或健康。`}
          />
        )}

        <div className="platform-fact-grid">
          <FactStat label="活动严重问题" value={overview.activeCriticalIssues} hint="可下钻到具体集群与资源" tone={overview.activeCriticalIssues > 0 ? 'crit' : 'ok'} />
          <FactStat label="受影响集群" value={overview.affectedClusters} hint={`纳管集群 ${overview.managedClusters} 个`} tone={overview.affectedClusters > 0 ? 'warn' : 'ok'} />
          <FactStat label="采集覆盖" value={`${overview.coverage.covered}/${overview.coverage.expected}`} hint={overview.coverage.ratio == null ? '覆盖率不可用' : `${(overview.coverage.ratio * 100).toFixed(1)}%`} tone={overview.coverage.ratio != null && overview.coverage.ratio >= 0.98 ? 'ok' : 'warn'} />
        </div>

        <PaneCard
          title="需要处理"
          action={<Tag>{overview.highestPriority ? '1 项最高优先级' : '0 项'}</Tag>}
          style={{ marginTop: 16 }}
        >
          {overview.highestPriority ? (
            <div className="platform-priority">
              <StatusBadge text="严重" tone="crit" />
              <h3>{overview.highestPriority.title}</h3>
              <p>影响对象：{overview.highestPriority.resourceUid || '未提供'} · 集群 {overview.highestPriority.clusterId || '未提供'}</p>
              <small>最近观察 {formatTime(overview.highestPriority.observedAt)}</small>
              <Button type="primary" style={{ marginTop: 16 }} onClick={() => navigate(`/ai-operations?sourcePage=/overview&cluster=${encodeURIComponent(overview.highestPriority!.clusterId)}`)}>
                交给 AI 分析
              </Button>
            </div>
          ) : (
            <Empty text="当前没有需要立即处理的问题" hint="告警与异常进入总览与集群，不设独立问题页" />
          )}
        </PaneCard>

        <PaneCard title="集群告警分布与健康状态" style={{ marginTop: 16 }} action={<Tag>共 {overview.managedClusters} 个纳管集群</Tag>}>
          <ClusterStateLine overview={overview} />
        </PaneCard>

        <PaneCard
          title="跨集群 CPU / 内存比较（集群口径）"
          style={{ marginTop: 16 }}
          action={<span className="muted-sm">CPU 已用核数 ÷ 可分配核数 · 内存 已用容量 ÷ 可分配容量</span>}
        >
          {capacityState === 'loading' && <div className="muted-sm">正在读取集群容量…</div>}
          {capacityState === 'error' && <Alert type="error" showIcon message="容量数据读取失败" description="本次探测未取得集群口径容量；不显示 0 或健康。" />}
          {capacityState === 'empty' && <Empty text="没有可用的集群容量事实" hint="服务端未返回任何纳管集群的容量数据" />}
          {capacityState === 'ready' && (
            <Table
              size="small"
              rowKey="clusterId"
              pagination={false}
              dataSource={capacity}
              columns={[
                {
                  title: '集群',
                  dataIndex: 'name',
                  key: 'name',
                  render: (v: string, r: CapacityClusterFact) => (
                    <button type="button" className="link-like" onClick={() => navigate(`/clusters/${encodeURIComponent(r.clusterId)}/overview`)}>
                      {v || r.clusterId}
                    </button>
                  ),
                },
                {
                  title: 'CPU 使用率',
                  key: 'cpu',
                  render: (_: unknown, r: CapacityClusterFact) => (
                    <Tooltip title={`${fmtCores(r.cpu.used)} / ${fmtCores(r.cpu.allocatable)} · 口径 ${r.cpu.aggregation} · 来源 ${r.cpu.source}`}>
                      <div><UtilizationBar ratio={r.cpu.usageRatio} label="CPU" /><small className="muted-sm">{fmtCores(r.cpu.used)} / {fmtCores(r.cpu.allocatable)}</small></div>
                    </Tooltip>
                  ),
                },
                {
                  title: '内存使用率',
                  key: 'mem',
                  render: (_: unknown, r: CapacityClusterFact) => (
                    <Tooltip title={`${fmtBytes(r.memory.used)} / ${fmtBytes(r.memory.allocatable)} · 口径 ${r.memory.aggregation} · 来源 ${r.memory.source}`}>
                      <div><UtilizationBar ratio={r.memory.usageRatio} label="内存" /><small className="muted-sm">{fmtBytes(r.memory.used)} / {fmtBytes(r.memory.allocatable)}</small></div>
                    </Tooltip>
                  ),
                },
                {
                  title: 'P95 / 最大节点',
                  key: 'p95',
                  render: (_: unknown, r: CapacityClusterFact) => (
                    <span>{fmtRatio(r.p95CpuUtilization === null ? null : r.p95CpuUtilization / 100)} / {fmtRatio(r.maxCpuUtilization === null ? null : r.maxCpuUtilization / 100)}</span>
                  ),
                },
                {
                  title: `热点节点（≥${hotThreshold(capacity)}%）`,
                  dataIndex: 'hotNodeCount',
                  key: 'hot',
                  render: (v: number, r: CapacityClusterFact) => v > 0
                    ? <StatusBadge text={`${v} 个`} tone="warn" />
                    : <span className="muted-sm">{r.quality === 'healthy' ? '0 个' : '未判定'}</span>,
                },
                {
                  title: '节点就绪',
                  key: 'nodes',
                  render: (_: unknown, r: CapacityClusterFact) => (
                    <span>{r.nodes.ready}/{r.nodes.total}{r.nodes.notReady > 0 ? `（未就绪 ${r.nodes.notReady}）` : ''}{r.nodes.unknown > 0 ? `（未知 ${r.nodes.unknown}）` : ''}</span>
                  ),
                },
                {
                  title: '数据质量',
                  key: 'quality',
                  render: (_: unknown, r: CapacityClusterFact) => (
                    <Tooltip title={r.qualityReason || '来源与口径完整'}>
                      <span><StatusBadge text={QUALITY_LABEL[r.quality] ?? r.quality} tone={QUALITY_TONE[r.quality] ?? 'muted'} /></span>
                    </Tooltip>
                  ),
                },
              ]}
            />
          )}
        </PaneCard>

        <PaneCard title="纳管集群清单" style={{ marginTop: 16 }} action={<Tag>{clusters.length} 个</Tag>}>
          {sortedClusters.length === 0 ? (
            <Empty text="暂无授权集群" hint="总览只展示当前账户有权查看的真实 Kubernetes 集群" />
          ) : (
            <div className="platform-cluster-list">
              {sortedClusters.map((cluster) => (
                <button type="button" className="platform-cluster-row" key={cluster.clusterId} onClick={() => navigate(`/clusters/${encodeURIComponent(cluster.clusterId)}/overview`)}>
                  <span className="platform-cluster-row__main">
                    <StatusBadge text={STATUS_LABELS[cluster.status]} tone={STATUS_TONES[cluster.status]} />
                    <strong>{cluster.name || cluster.clusterId}</strong>
                    <span className="platform-cluster-row__reason">{cluster.statusReason || '缺少状态原因'}</span>
                    <span className="platform-cluster-row__registration">{registrationLabel(cluster.registrationStatus)}</span>
                  </span>
                  <span className="platform-cluster-row__time">最新 {formatTime(cluster.updatedAt)}</span>
                  <span aria-hidden>→</span>
                </button>
              ))}
            </div>
          )}
        </PaneCard>

        <PaneCard title="数据新鲜度与平台能力" style={{ marginTop: 16 }}>
          <div className="platform-timeline-facts">
            <FactStat label="最新数据" value={formatTime(overview.freshestAt)} />
            <FactStat label="最旧有效数据" value={formatTime(overview.oldestValidAt)} />
            <FactStat label="未知/陈旧集群" value={overview.unknownOrStaleClusters} tone={overview.unknownOrStaleClusters > 0 ? 'warn' : 'ok'} />
          </div>
          <div className="platform-capability">
            <strong>{capabilitySummaryText(overview.capabilitySummary.healthy, overview.capabilitySummary.total)}</strong>
            <span>{overview.capabilitySummary.total > 0 ? '已读取的能力状态' : '组件探测结果尚不可用'}</span>
            {overview.capabilitySummary.issues.map((issue) => <Tag color="warning" key={issue}>{issue}</Tag>)}
            <Button type="link" onClick={() => navigate('/settings?section=health')}>在设置查看平台自身健康</Button>
          </div>
        </PaneCard>
      </>}
    </BoundedDataRegion>
  </div>
}

function hotThreshold(facts: CapacityClusterFact[]): number {
  const first = facts.find((f) => Number.isFinite(f.hotNodeThresholdPct))
  return first ? first.hotNodeThresholdPct : 80
}

export default Overview
