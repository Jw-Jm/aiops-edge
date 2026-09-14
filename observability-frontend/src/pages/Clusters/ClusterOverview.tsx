import React, { useEffect, useState } from 'react'
import { Alert, Button, Col, Row, Table, Tag, Tooltip } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import { getClusterOverview, type ClusterOverview as ClusterOverviewData, type FoundationFact, type ResourceKindSummary } from '../../api/clusterOverview'
import { getClusterRuntime, type ClusterRuntime } from '../../api/clusterRuntime'
import { getPlatformCapacity, type CapacityClusterFact } from '../../api/platform'
import { listHpa, type HpaFact } from '../../api/telemetry'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import { Empty, PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import type { PlatformClusterHealth } from '../../api/platform'
import { CLUSTER_HEALTH_LABELS, CLUSTER_HEALTH_TONES, registrationLabel } from '../../features/platform/healthPresentation'

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

const STATUS_LABELS = CLUSTER_HEALTH_LABELS
const STATUS_TONES = CLUSTER_HEALTH_TONES

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

function fmtCores(v: number): string {
  return `${Number.isFinite(v) ? v.toFixed(2) : '未提供'} 核`
}

function fmtBytes(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return '未提供'
  return `${(v / 1024 ** 3).toFixed(2)} GiB`
}

function fmtPct(ratio: number | null): string {
  return ratio === null || ratio === undefined ? '未提供' : `${(ratio * 100).toFixed(1)}%`
}

function UtilBar({ ratio, label }: { ratio: number | null; label: string }) {
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

function DataStateInline({ text }: { text: string }) {
  return <Alert type="warning" showIcon message={text} />
}

function SignalGap({ title, gap }: { title: string; gap: { quality: string; reason: string } }) {
  return (
    <div className="cluster-signal-gap">
      <div className="cluster-signal-gap__head">
        <span>{title}</span>
        <StatusBadge text={QUALITY_LABEL[gap.quality] ?? gap.quality} tone={QUALITY_TONE[gap.quality] ?? 'muted'} />
      </div>
      <p className="muted-sm">{gap.reason || '来源未接入，无法给出该信号'}</p>
    </div>
  )
}

const ClusterOverview: React.FC = () => {
  const { clusterUid = '' } = useParams<{ clusterUid: string }>()
  const navigate = useNavigate()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const timeRange = useScopeStore((s) => s.active.timeRange)
  const [data, setData] = useState<ClusterOverviewData | null>(null)
  const [capacity, setCapacity] = useState<CapacityClusterFact | null>(null)
  const [runtime, setRuntime] = useState<ClusterRuntime | null>(null)
  const [hpa, setHpa] = useState<HpaFact[]>([])
  const [hpaState, setHpaState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('loading')
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [error, setError] = useState<unknown>()

  const load = () => {
    if (!clusterUid) { setState('empty'); return () => undefined }
    const controller = new AbortController()
    setState('loading')
    setError(undefined)
    setCapacity(null)
    setRuntime(null)

    void getClusterOverview(clusterUid, controller.signal).then((result) => {
      setData(result)
      setState(result.meta.partial ? 'partial' : result.meta.stale ? 'stale' : 'ready')
    }).catch((reason) => {
      if (reason?.response?.status === 403) setState('forbidden')
      else { setError(reason); setState('error') }
    })

    // 集群口径容量：同一后端能力，按 clusterUid 取该集群
    getPlatformCapacity(controller.signal)
      .then((res) => setCapacity(res.clusters.find((c) => c.clusterId === clusterUid) ?? null))
      .catch(() => setCapacity(null))

    getClusterRuntime(clusterUid, controller.signal)
      .then(setRuntime)
      .catch(() => setRuntime(null))

    setHpaState('loading')
    listHpa(controller.signal)
      .then((res) => {
        setHpa(res.items)
        setHpaState(res.error ? 'error' : res.items.length === 0 ? 'empty' : 'ready')
      })
      .catch((err) => setHpaState(err?.response?.status === 403 ? 'forbidden' : 'error'))

    return () => controller.abort()
  }

  useEffect(() => load(), [clusterUid])

  const timeWindowText =
    timeRange.mode === 'absolute'
      ? `${formatTime(timeRange.start)} → ${formatTime(timeRange.end)}`
      : `最近 ${timeRange.minutes >= 60 ? `${timeRange.minutes / 60} 小时` : `${timeRange.minutes} 分钟`}`

  return (
    <div className="cluster-overview-page" data-testid="cluster-page">
      {data ? (
        <>
          <PageHeader
            title="集群"
            desc={(
              <span data-testid="cluster-identity">
                集群 {data.name || data.clusterId} · 环境 {data.environment || '未提供'} · 地域 {data.region || '未提供'} ·
                Kubernetes {data.version || '未提供'} · 时间窗 {timeWindowText} · 数据截止 {formatTime(data.meta.generatedAt)}
              </span>
            )}
            actions={(
              <div className="page-actions">
                <Button onClick={load}>刷新</Button>
                <Button type="primary" onClick={() => navigate(`/ai-operations?sourcePage=/clusters&cluster=${encodeURIComponent(clusterUid)}`)}>
                  交给 AI 分析
                </Button>
              </div>
            )}
          />
          <h2 className="cluster-overview-cluster-name">{data.name || data.clusterId}</h2>
          <div className="cluster-overview-statusline">
            <StatusBadge text={STATUS_LABELS[data.status]} tone={STATUS_TONES[data.status]} />
            {data.statusReasons.map((reason) => <span key={reason} className="cluster-overview-statusline__reason">{reason}</span>)}
            <span className="cluster-overview-statusline__registration">{registrationLabel(data.registrationStatus)}</span>
            <span className="cluster-overview-statusline__meta">采集覆盖 {data.coverage.covered}/{data.coverage.expected}</span>
            <code>{data.clusterId}</code>
          </div>
        </>
      ) : (
        <PageHeader title="集群" desc={clusterUid ? `集群 ${clusterUid}` : '未选择集群'} />
      )}

      <BoundedDataRegion state={state} error={error} onRetry={load} meta={data?.meta}>
        {data && (
          <>
            {/* 集群口径 CPU / 内存 + P95 / 最大值 / 热点节点（§6.3） */}
            <PaneCard
              title="集群 CPU 与内存（集群口径）"
              action={<span className="muted-sm">使用量 ÷ 可分配量</span>}
            >
              {!capacity ? (
                <Alert
                  type="warning"
                  showIcon
                  message="容量事实不可用"
                  description="服务端未返回该集群的容量数据；此处不显示 0 或健康值。"
                />
              ) : capacity.quality === 'not_connected' || capacity.quality === 'failed' ? (
                <Alert
                  type="warning"
                  showIcon
                  message={`容量来源${QUALITY_LABEL[capacity.quality] ?? capacity.quality}`}
                  description={capacity.qualityReason || '该集群未接入指标读取通道'}
                />
              ) : (
                <>
                  <Row gutter={[16, 16]}>
                    <Col xs={24} md={12}>
                      <div className="cluster-capacity-block">
                        <div className="cluster-capacity-block__head"><strong>CPU</strong><span className="muted-sm">单位 核 · 口径 {capacity.cpu.aggregation}</span></div>
                        <UtilBar ratio={capacity.cpu.usageRatio} label="CPU" />
                        <div className="muted-sm">{fmtCores(capacity.cpu.used)} / {fmtCores(capacity.cpu.allocatable)}</div>
                      </div>
                    </Col>
                    <Col xs={24} md={12}>
                      <div className="cluster-capacity-block">
                        <div className="cluster-capacity-block__head"><strong>内存</strong><span className="muted-sm">单位 字节(GiB 展示) · 口径 {capacity.memory.aggregation}</span></div>
                        <UtilBar ratio={capacity.memory.usageRatio} label="内存" />
                        <div className="muted-sm">{fmtBytes(capacity.memory.used)} / {fmtBytes(capacity.memory.allocatable)}</div>
                      </div>
                    </Col>
                  </Row>
                  <Table
                    size="small"
                    style={{ marginTop: 12 }}
                    pagination={false}
                    rowKey="metric"
                    dataSource={[
                      { metric: 'P95 节点 CPU 利用率', value: capacity.p95CpuUtilization === null ? '未提供' : `${capacity.p95CpuUtilization.toFixed(1)}%` },
                      { metric: '最大节点 CPU 利用率', value: capacity.maxCpuUtilization === null ? '未提供' : `${capacity.maxCpuUtilization.toFixed(1)}%` },
                      { metric: 'P95 节点内存利用率', value: capacity.p95MemUtilization === null ? '未提供' : `${capacity.p95MemUtilization.toFixed(1)}%` },
                      { metric: `热点节点数（≥${capacity.hotNodeThresholdPct}%）`, value: `${capacity.hotNodeCount} 个` },
                      { metric: '节点就绪', value: `${capacity.nodes.ready}/${capacity.nodes.total}${capacity.nodes.notReady > 0 ? `（未就绪 ${capacity.nodes.notReady}）` : ''}${capacity.nodes.unknown > 0 ? `（未知 ${capacity.nodes.unknown}）` : ''}` },
                      { metric: '数据来源', value: capacity.cpu.source },
                      { metric: '来源时间', value: formatTime(capacity.cpu.sourceTimestamp) },
                    ]}
                    columns={[
                      { title: '指标', dataIndex: 'metric', key: 'metric', width: 260 },
                      { title: '值', dataIndex: 'value', key: 'value' },
                    ]}
                  />
                </>
              )}
            </PaneCard>

            {/* Pod 调度与重启（§6.3 固定图表） */}
            <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
              <Col xs={24} lg={12}>
                <PaneCard title="Pod 调度与就绪" action={<span className="muted-sm">{runtime?.source ?? '来源未提供'}</span>}>
                  {!runtime ? (
                    <Alert type="warning" showIcon message="Pod 事实不可用" description="服务端未返回该集群的 Pod 事实；此处不显示 0 或健康值。" />
                  ) : runtime.quality === 'not_connected' || runtime.quality === 'failed' ? (
                    <Alert
                      type="warning"
                      showIcon
                      message={`Pod 来源${QUALITY_LABEL[runtime.quality] ?? runtime.quality}`}
                      description={runtime.qualityNote || '该集群未接入运行时读取通道'}
                    />
                  ) : (
                    <div className="kubevirt-summary-grid">
                      <div><strong>{runtime.pods.total}</strong><span>Pod 总数</span></div>
                      <div><strong>{runtime.pods.running}</strong><span>Running</span></div>
                      <div><strong>{runtime.pods.ready}</strong><span>容器就绪</span></div>
                      <div><strong>{runtime.pods.notReady}</strong><span>容器未就绪</span></div>
                      <div><strong>{runtime.pods.pending}</strong><span>Pending</span></div>
                      <div><strong>{runtime.pods.failed}</strong><span>Failed</span></div>
                    </div>
                  )}
                </PaneCard>
              </Col>
              <Col xs={24} lg={12}>
                <PaneCard title="容器重启 Top" action={<span className="muted-sm">{runtime ? `共 ${runtime.restarts.totalRestarts} 次` : '—'}</span>}>
                  {!runtime ? (
                    <Alert type="warning" showIcon message="重启事实不可用" description="未取得 Pod 容器状态，不显示 0 或健康。" />
                  ) : runtime.quality === 'not_connected' || runtime.quality === 'failed' ? (
                    <Alert type="warning" showIcon message="重启事实不可用" description={runtime.qualityNote || '该集群未接入运行时读取通道'} />
                  ) : runtime.restarts.top.length === 0 ? (
                    <Empty text="当前没有容器重启记录" hint="来源：core/v1 pods.status.containerStatuses[].restartCount" />
                  ) : (
                    <Table
                      size="small"
                      rowKey={(r) => `${r.namespace}/${r.name}`}
                      pagination={false}
                      dataSource={runtime.restarts.top}
                      columns={[
                        { title: '命名空间', dataIndex: 'namespace', key: 'namespace', width: 140 },
                        { title: 'Pod', dataIndex: 'name', key: 'name' },
                        { title: '重启次数', dataIndex: 'restarts', key: 'restarts', width: 100 },
                      ]}
                    />
                  )}
                </PaneCard>
              </Col>
            </Row>

            {/* 网络与存储：未接入时显式表达（§6.3 禁止用 API 推算不存在的时序指标） */}
            <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
              <Col xs={24} lg={12}><PaneCard title="网络吞吐 / 错误 / 丢包 / 时延"><SignalGap title="网络信号" gap={runtime?.network ?? { quality: 'not_connected', reason: '未取得网络信号来源' }} /></PaneCard></Col>
              <Col xs={24} lg={12}><PaneCard title="存储使用率 / 容量 / IO 时延"><SignalGap title="存储信号" gap={runtime?.storage ?? { quality: 'not_connected', reason: '未取得存储信号来源' }} /></PaneCard></Col>
            </Row>

            {/* 自动扩缩容事实：副本目标与实际偏移是容量风险的可验证依据 */}
            <PaneCard title="工作负载自动扩缩容（HPA）" style={{ marginTop: 16 }} action={<span className="muted-sm">{hpa.length} 项</span>}>
              {hpaState === 'loading' && <span className="muted-sm">正在读取 HPA…</span>}
              {hpaState === 'forbidden' && <DataStateInline text="当前账户没有读取 HPA 的权限" />}
              {hpaState === 'error' && <DataStateInline text="HPA 读取失败，可能未安装指标适配器；不显示 0 或健康" />}
              {hpaState === 'empty' && <Empty text="当前集群没有 HPA 对象" hint="来源：autoscaling/v1 horizontalpodautoscalers" />}
              {hpaState === 'ready' && (
                <Table
                  size="small"
                  rowKey={(r) => `${r.namespace}/${r.name}`}
                  pagination={{ pageSize: 8 }}
                  dataSource={hpa}
                  columns={[
                    { title: '命名空间', dataIndex: 'namespace', key: 'namespace', width: 160 },
                    { title: '对象', dataIndex: 'name', key: 'name' },
                    { title: '当前/期望副本', key: 'replicas', width: 140, render: (_: unknown, r: HpaFact) => `${r.currentReplicas ?? '未提供'} / ${r.desiredReplicas ?? '未提供'}` },
                    { title: '最小/最大副本', key: 'bounds', width: 140, render: (_: unknown, r: HpaFact) => `${r.minReplicas ?? '未提供'} / ${r.maxReplicas ?? '未提供'}` },
                    { title: '指标', dataIndex: 'metricName', key: 'metricName', width: 120 },
                    { title: '利用率', dataIndex: 'utilization', key: 'utilization', width: 100, render: (v: number | null) => (v === null ? '未提供' : `${v}%`) },
                  ]}
                />
              )}
            </PaneCard>

            {/* 活动告警：点击进入 AI 智能运维（§6.3，不进入独立问题页） */}
            <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
              <Col xs={24} lg={16}>
                <PaneCard title="活动告警" action={<Tag color={data.issues.length ? 'red' : 'green'}>{data.issues.length} 项</Tag>}>
                  {data.issues.length ? (
                    <Table
                      size="small"
                      rowKey={(r) => `${r.resourceUid}:${r.ruleId}`}
                      pagination={{ pageSize: 6 }}
                      dataSource={data.issues}
                      onRow={(record) => ({
                        onClick: () =>
                          navigate(
                            `/ai-operations?sourcePage=/clusters&cluster=${encodeURIComponent(clusterUid)}&resourceUid=${encodeURIComponent(record.resourceUid)}&severity=${encodeURIComponent(record.severity)}`,
                          ),
                        style: { cursor: 'pointer' },
                      })}
                      columns={[
                        { title: '严重度', dataIndex: 'severity', key: 'severity', width: 90, render: (v: string) => <StatusBadge text={v === 'critical' ? '严重' : v} tone={v === 'critical' ? 'crit' : 'warn'} /> },
                        { title: '对象', dataIndex: 'resourceUid', key: 'resourceUid', width: 200 },
                        { title: '标题', dataIndex: 'title', key: 'title' },
                        { title: '规则', dataIndex: 'ruleId', key: 'ruleId', width: 160 },
                        { title: '开始/观察时间', dataIndex: 'observedAt', key: 'observedAt', width: 200, render: (v?: string) => formatTime(v) },
                      ]}
                    />
                  ) : (
                    <Empty text="当前集群没有活动告警" hint="告警进入集群与总览，点击告警将携带上下文进入 AI 智能运维" />
                  )}
                </PaneCard>
              </Col>
              <Col xs={24} lg={8}>
                <PaneCard title="数据质量与覆盖">
                  <div className="cluster-quality-summary">
                    <strong>{data.coverage.covered}/{data.coverage.expected}</strong>
                    <span>采集覆盖</span>
                    <small>数据截止 {formatTime(data.meta.generatedAt)}</small>
                  </div>
                  {data.meta.warningCodes.length > 0 && (
                    <Tooltip title={data.meta.warningCodes.join(' · ')}>
                      <span className="muted-sm">存在 {data.meta.warningCodes.length} 项数据质量提示（技术详情）</span>
                    </Tooltip>
                  )}
                  <Button type="link" onClick={() => navigate('/settings?section=health')}>在设置查看平台自身健康</Button>
                </PaneCard>
              </Col>
            </Row>

            <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
              <Col xs={24} lg={12}>
                <PaneCard title="Kubernetes 资源" action={<Button type="link" onClick={() => navigate(`/clusters/${encodeURIComponent(clusterUid)}/overview?tab=resources`)}>在资源上下文查看 →</Button>}>
                  <div className="cluster-resource-kind-grid">
                    {data.resourceKinds.map((item) => (
                      <div className="cluster-resource-kind" key={item.kind}>
                        <div className="cluster-resource-kind__label">{KIND_LABELS[item.kind]}</div>
                        <div className="cluster-resource-kind__value">{item.total}</div>
                        <div className="cluster-resource-kind__meta">异常 {item.abnormal} · 未知/陈旧 {item.unknownOrStale}</div>
                      </div>
                    ))}
                  </div>
                </PaneCard>
              </Col>
              <Col xs={24} lg={12}>
                <PaneCard title="KubeVirt 虚拟机">
                  <div className="kubevirt-summary-grid">
                    <div><strong>{data.kubevirt.vm}</strong><span>VM</span></div>
                    <div><strong>{data.kubevirt.vmi}</strong><span>VMI</span></div>
                    <div><strong>{data.kubevirt.notReady}</strong><span>未就绪</span></div>
                    <div><strong>{data.kubevirt.migrating + data.kubevirt.failedMigration}</strong><span>迁移异常</span></div>
                    <div><strong>{data.kubevirt.storageAffected + data.kubevirt.networkAffected}</strong><span>基础能力影响</span></div>
                  </div>
                </PaneCard>
              </Col>
            </Row>

            <PaneCard title="集群基础能力" style={{ marginTop: 16 }}>
              <div className="cluster-foundation-grid">
                {(['control_plane', 'nodes_hosts', 'network', 'storage', 'kubevirt'] as FoundationFact['kind'][]).map((kind) => {
                  const item = data.foundation.find((f) => f.kind === kind) ?? { kind, status: 'unknown' as const, reason: '暂无证据', affectedResourceCount: 0 }
                  return (
                    <div className="cluster-foundation-fact" key={kind}>
                      <div className="cluster-foundation-fact__head">
                        <span>{FOUNDATION_LABELS[kind]}</span>
                        <StatusBadge text={STATUS_LABELS[item.status]} tone={STATUS_TONES[item.status]} />
                      </div>
                      <p>{item.reason}</p>
                      <small>影响资源 {item.affectedResourceCount}</small>
                    </div>
                  )
                })}
              </div>
            </PaneCard>

            {activeClusterId && activeClusterId !== clusterUid && (
              <Alert type="warning" showIcon style={{ marginTop: 12 }} message="URL 集群与当前作用域不一致" description={`顶栏作用域为 ${activeClusterId}，本页为 ${clusterUid}。切换作用域前本页只读。`} />
            )}
          </>
        )}
      </BoundedDataRegion>
    </div>
  )
}

export default ClusterOverview
