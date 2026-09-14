import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Input, Select, Table, Tag } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getObservabilityPath, getObservabilityPaths, type PathDetail } from '../../api/observability'
import { queryRawLogs, queryServiceMetrics, type RawLogRow, type TypedMetricPoint } from '../../api/telemetry'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import DataState from '../../components/display/DataState'
import { Empty, PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import { qualityLabel, selectPath, type DataQuality, type ObservabilityPath } from './pathSelection'

const QUALITY_TONE: Record<DataQuality, StatusTone> = {
  healthy: 'ok',
  partial: 'warn',
  stale: 'warn',
  unknown: 'muted',
  not_connected: 'muted',
  failed: 'crit',
}

function formatTime(value?: string | null): string {
  if (!value) return '未提供'
  const t = Date.parse(value)
  return Number.isFinite(t) ? new Date(t).toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' }) : value
}

function fmtNum(v?: number | null, unit = ''): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return '未提供'
  return `${v.toFixed(v >= 100 ? 0 : 2)}${unit}`
}

const Observability: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')

  const [catalog, setCatalog] = useState<Awaited<ReturnType<typeof getObservabilityPaths>> | null>(null)
  const [catalogState, setCatalogState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [catalogError, setCatalogError] = useState<unknown>()
  const [detail, setDetail] = useState<PathDetail | null>(null)
  const [detailState, setDetailState] = useState<'idle' | 'loading' | 'ready' | 'error' | 'forbidden'>('idle')
  const [logs, setLogs] = useState<RawLogRow[]>([])
  const [logsState, setLogsState] = useState<'idle' | 'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('idle')
  const [serviceMetrics, setServiceMetrics] = useState<TypedMetricPoint | null>(null)
  const [metricsState, setMetricsState] = useState<'idle' | 'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('idle')
  const [search, setSearch] = useState('')
  const [categoryFilter, setCategoryFilter] = useState<string | undefined>()
  const [statusFilter, setStatusFilter] = useState<string | undefined>()

  const loadCatalog = useCallback(() => {
    // 范围未建立时不得发起集群级请求：否则服务端按契约 fail-closed，
    // 首屏会出现与真实状态无关的 409/SCOPE_SELECTION_REQUIRED。
    if (!activeClusterId) {
      setCatalog(null)
      setCatalogState('empty')
      return () => undefined
    }
    const controller = new AbortController()
    setCatalogState('loading')
    getObservabilityPaths(controller.signal)
      .then((data) => {
        setCatalog(data)
        if (data.paths.length === 0) setCatalogState('empty')
        else if (data.partial) setCatalogState('partial')
        else if (data.stale) setCatalogState('stale')
        else setCatalogState('ready')
      })
      .catch((error) => {
        setCatalogError(error)
        setCatalogState(error?.response?.status === 403 ? 'forbidden' : 'error')
      })
    return () => controller.abort()
  }, [activeClusterId])

  useEffect(() => loadCatalog(), [loadCatalog])

  const paths: ObservabilityPath[] = catalog?.paths ?? []

  const filtered = useMemo(
    () =>
      paths.filter((p) => {
        if (search && !`${p.name} ${p.pathId} ${p.category}`.toLowerCase().includes(search.toLowerCase())) return false
        if (categoryFilter && p.category !== categoryFilter) return false
        if (statusFilter && p.status !== statusFilter) return false
        return true
      }),
    [paths, search, categoryFilter, statusFilter],
  )

  const selection = useMemo(
    () => selectPath(filtered, searchParams.get('selectedPathId')),
    [filtered, searchParams],
  )

  // 切换路径：URL 与全部面板原子更新
  useEffect(() => {
    if (!selection.selectedPathId) return
    if (searchParams.get('selectedPathId') !== selection.selectedPathId) {
      const next = new URLSearchParams(searchParams)
      next.set('selectedPathId', selection.selectedPathId)
      setSearchParams(next, { replace: true })
    }
  }, [selection.selectedPathId, searchParams, setSearchParams])

  const selectedPathId = selection.selectedPathId

  useEffect(() => {
    if (!selectedPathId || !activeClusterId) {
      setDetail(null)
      setDetailState('idle')
      return
    }
    const controller = new AbortController()
    setDetailState('loading')
    setDetail(null)
    getObservabilityPath(selectedPathId, controller.signal)
      .then((data) => {
        setDetail(data)
        setDetailState('ready')
      })
      .catch((error) => {
        setDetailState(error?.response?.status === 403 ? 'forbidden' : 'error')
      })
    return () => controller.abort()
  }, [selectedPathId])

  // 原始日志：与所选路径同一 scope 与时间窗（只传受限标签，不构造查询语言）
  useEffect(() => {
    if (!activeClusterId) {
      setLogs([])
      setLogsState('idle')
      return
    }
    const controller = new AbortController()
    setLogsState('loading')
    queryRawLogs({ minutes: 60, limit: 50 }, controller.signal)
      .then((rows) => {
        setLogs(rows)
        setLogsState(rows.length === 0 ? 'empty' : 'ready')
      })
      .catch((error) => setLogsState(error?.response?.status === 403 ? 'forbidden' : 'error'))
    return () => controller.abort()
  }, [activeClusterId])

  // 类型化服务指标：任意 PromQL 直通已关闭，这里只能按 service 名称查询
  const metricTarget = detail?.nodes?.find((n) => /service/i.test(String(n.node_type)))?.name ?? detail?.nodes?.[0]?.name ?? ''
  useEffect(() => {
    if (!activeClusterId || !metricTarget) {
      setServiceMetrics(null)
      setMetricsState('idle')
      return
    }
    const controller = new AbortController()
    setMetricsState('loading')
    queryServiceMetrics({ clusterId: activeClusterId, service: metricTarget }, controller.signal)
      .then((res) => {
        setServiceMetrics(res)
        setMetricsState(res.values.length === 0 ? 'empty' : 'ready')
      })
      .catch((error) => setMetricsState(error?.response?.status === 403 ? 'forbidden' : 'error'))
    return () => controller.abort()
  }, [activeClusterId, metricTarget])

  const categories = Array.from(new Set(paths.map((p) => p.category)))
  const statuses = Array.from(new Set(paths.map((p) => p.status)))
  const selectedPath = paths.find((p) => p.pathId === selectedPathId) ?? null

  return (
    <div className="observability-page" data-testid="observability-page">
      <PageHeader
        title="全链路监控"
        desc="云平台控制面、计算、网络、存储与 Kubernetes 依赖路径；所有面板绑定同一 selectedPathId"
        actions={<Button onClick={loadCatalog}>刷新路径目录</Button>}
      />

      <BoundedDataRegion
        state={catalogState}
        error={catalogError}
        onRetry={loadCatalog}
        meta={{ partial: catalog?.partial, stale: catalog?.stale, generatedAt: catalog?.generated_at }}
      >
        <>
          {(catalog?.partial || catalog?.stale) && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 12 }}
              message={catalog?.partial ? '路径目录部分来源不可用' : '路径目录数据已过期'}
              description={catalog?.quality_reason ?? '部分来源未返回数据，以下路径与指标可能不完整。'}
            />
          )}

          {/* 顶部固定显示当前路径、pathId、范围、选择原因、时间窗与数据质量（§6.4） */}
          <div className="path-header card" data-testid="path-header">
            <div className="path-header__row">
              <div>
                <span className="muted-sm">当前路径</span>
                <h2>{selectedPath?.name ?? '未选择路径'}</h2>
              </div>
              <div>
                <span className="muted-sm">selectedPathId</span>
                <code>{selectedPathId ?? '—'}</code>
              </div>
              <div>
                <span className="muted-sm">作用域</span>
                <code>{activeClusterId || '未选择集群'}</code>
              </div>
              <div>
                <span className="muted-sm">时间窗</span>
                <span>
                  {catalog ? `${formatTime(catalog.time_window.from)} → ${formatTime(catalog.time_window.to)}` : '未提供'}
                </span>
              </div>
              <div>
                <span className="muted-sm">数据质量</span>
                {selectedPath ? (
                  <StatusBadge text={qualityLabel(selectedPath.quality)} tone={QUALITY_TONE[selectedPath.quality]} />
                ) : (
                  <StatusBadge text="未提供" tone="muted" />
                )}
              </div>
            </div>
            <p className="path-header__reason" data-testid="path-selection-reason">
              <strong>选择原因：</strong>
              {selection.reason}
            </p>
            {selection.factors.length > 0 && (
              <div className="path-header__factors">
                {selection.factors.map((f) => (
                  <Tag key={f.label}>
                    {f.label} {f.value}（权重 {f.weight}）
                  </Tag>
                ))}
              </div>
            )}
          </div>

          <div className="observability-grid">
            <PaneCard title="路径目录" action={<span className="muted-sm">{filtered.length}/{paths.length}</span>}>
              <div className="path-filters">
                <Input.Search allowClear placeholder="搜索路径名称或 pathId" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="搜索路径" />
                <Select allowClear placeholder="路径类型" value={categoryFilter} onChange={setCategoryFilter} options={categories.map((c) => ({ value: c, label: c }))} aria-label="按路径类型过滤" />
                <Select allowClear placeholder="状态" value={statusFilter} onChange={setStatusFilter} options={statuses.map((s) => ({ value: s, label: s }))} aria-label="按状态过滤" />
              </div>
              {filtered.length === 0 ? (
                <DataState kind={paths.length === 0 ? 'empty' : 'empty'} title={paths.length === 0 ? '路径目录为空' : '当前筛选没有匹配路径'} description={paths.length === 0 ? '服务端未返回任何云平台路径，可能是数据源未接入' : '请调整搜索或筛选条件'} />
              ) : (
                <ul className="path-list" data-testid="path-list">
                  {filtered.map((p) => (
                    <li key={p.pathId}>
                      <button
                        type="button"
                        className={p.pathId === selectedPathId ? 'is-active' : ''}
                        aria-current={p.pathId === selectedPathId ? 'true' : undefined}
                        onClick={() => {
                          const next = new URLSearchParams(searchParams)
                          next.set('selectedPathId', p.pathId)
                          setSearchParams(next, { replace: true })
                        }}
                      >
                        <span className="path-list__main">
                          <strong>{p.name}</strong>
                          <small>
                            {p.category} · {p.pathId}
                          </small>
                        </span>
                        <StatusBadge text={qualityLabel(p.quality)} tone={QUALITY_TONE[p.quality]} />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </PaneCard>

            <PaneCard title="所选路径：端到端依赖与实时状态" action={<span className="muted-sm">{detail ? `${detail.nodes.length} 节点 / ${detail.edges.length} 边` : '—'}</span>}>
              {detailState === 'loading' && <DataState kind="loading" />}
              {detailState === 'error' && <DataState kind="error" description="该路径的依赖视图读取失败" onRetry={() => selectedPathId && getObservabilityPath(selectedPathId).then(setDetail).catch(() => undefined)} />}
              {detailState === 'forbidden' && <DataState kind="forbidden" />}
              {detailState === 'ready' && detail && (
                <>
                  {detail.gaps.length > 0 && (
                    <Alert type="warning" showIcon message="该路径存在证据缺口" description={<ul>{detail.gaps.map((g) => <li key={g}>{g}</li>)}</ul>} style={{ marginBottom: 8 }} />
                  )}
                  {detail.nodes.length === 0 ? (
                    <DataState kind="empty" title="该路径暂无依赖节点" description="来源系统未返回节点数据" />
                  ) : (
                    <Table
                      size="small"
                      rowKey="node_uid"
                      pagination={false}
                      dataSource={detail.nodes}
                      columns={[
                        { title: '节点', dataIndex: 'name', key: 'name', width: 220 },
                        { title: '类型', dataIndex: 'node_type', key: 'node_type', width: 120 },
                        { title: '状态', dataIndex: 'status', key: 'status', width: 100 },
                        { title: '延迟 (ms)', dataIndex: 'latency_ms', key: 'latency', width: 100, render: (v: number | null) => fmtNum(v, ' ms') },
                        { title: '错误率', dataIndex: 'error_rate', key: 'error_rate', width: 100, render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(2)}%`) },
                        { title: '饱和度', dataIndex: 'saturation', key: 'saturation', width: 100, render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(0)}%`) },
                        { title: '质量', dataIndex: 'quality', key: 'quality', width: 100 },
                      ]}
                    />
                  )}
                </>
              )}
            </PaneCard>

            <PaneCard title="所选路径：关键指标趋势">
              {detailState === 'ready' && detail ? (
                <>
                  <p className="muted-sm">
                    指标 {detail.trend.metric} · 单位 {detail.trend.unit} · 聚合 {detail.trend.aggregation} · 步长 {detail.trend.step_seconds}s
                  </p>
                  {detail.trend.points.length === 0 ? (
                    <DataState kind="empty" title="该路径暂无趋势数据" />
                  ) : (
                    <Table
                      size="small"
                      rowKey="ts"
                      pagination={{ pageSize: 8 }}
                      dataSource={detail.trend.points}
                      columns={[
                        { title: '时间', dataIndex: 'ts', key: 'ts', render: formatTime },
                        { title: '延迟 (ms)', dataIndex: 'latency_ms', key: 'latency_ms', render: (v: number | null) => fmtNum(v) },
                        { title: '错误率', dataIndex: 'error_rate', key: 'error_rate', render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(2)}%`) },
                        { title: '吞吐', dataIndex: 'throughput', key: 'throughput', render: (v: number | null) => fmtNum(v) },
                        { title: '饱和度', dataIndex: 'saturation', key: 'saturation', render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(0)}%`) },
                      ]}
                    />
                  )}
                </>
              ) : (
                <DataState kind="loading" />
              )}
            </PaneCard>

            <PaneCard title="关键路径性能矩阵" action={<span className="muted-sm">{detail ? `${detail.segments.length} 段` : '—'}</span>}>
              {detailState === 'ready' && detail ? (
                detail.segments.length === 0 ? (
                  <DataState kind="empty" title="该路径暂无分段性能数据" />
                ) : (
                  <Table
                    size="small"
                    rowKey="segment"
                    pagination={false}
                    dataSource={detail.segments}
                    columns={[
                      { title: '段', dataIndex: 'segment', key: 'segment', width: 180 },
                      { title: '延迟贡献 (ms)', dataIndex: 'latency_ms', key: 'latency_ms', render: (v: number | null) => fmtNum(v) },
                      { title: '错误率', dataIndex: 'error_rate', key: 'error_rate', render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(2)}%`) },
                      { title: '饱和度', dataIndex: 'saturation', key: 'saturation', render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(0)}%`) },
                      { title: '相对基线', dataIndex: 'baseline_delta', key: 'baseline_delta', render: (v: number | null) => (v === null || v === undefined ? '未提供' : `${(v * 100).toFixed(1)}%`) },
                      { title: '样本量', dataIndex: 'sample_size', key: 'sample_size', render: (v: number | null) => (v ?? '未提供') },
                      { title: '质量', dataIndex: 'quality', key: 'quality' },
                    ]}
                  />
                )
              ) : (
                <DataState kind="loading" />
              )}
            </PaneCard>

            <PaneCard title="可打开的事件与变更证据" action={<span className="muted-sm">{detail ? `${detail.events.length} 条` : '—'}</span>}>
              {detailState === 'ready' && detail ? (
                detail.events.length === 0 ? (
                  <DataState kind="empty" title="该路径窗口内没有事件或变更证据" />
                ) : (
                  <Table
                    size="small"
                    rowKey="evidence_id"
                    pagination={{ pageSize: 8 }}
                    dataSource={detail.events}
                    columns={[
                      { title: 'evidence_id', dataIndex: 'evidence_id', key: 'evidence_id', render: (v: string) => <code>{v}</code> },
                      { title: '类型', dataIndex: 'type', key: 'type', width: 100 },
                      { title: '摘要', dataIndex: 'summary', key: 'summary' },
                      { title: '发生时间', dataIndex: 'occurred_at', key: 'occurred_at', render: formatTime, width: 200 },
                      { title: '质量', dataIndex: 'quality', key: 'quality', width: 90 },
                    ]}
                  />
                )
              ) : (
                <DataState kind="loading" />
              )}
            </PaneCard>

            <PaneCard title="服务关键指标（类型化指标）" action={<span className="muted-sm">{metricTarget ? `服务 ${metricTarget}` : '未选择服务'}</span>}>
              {metricsState === 'idle' && <DataState kind="empty" title="没有可用于查询指标的服务对象" description="类型化指标需要明确的 service 名称；任意 PromQL 直通已按设计关闭" />}
              {metricsState === 'loading' && <DataState kind="loading" />}
              {metricsState === 'forbidden' && <DataState kind="forbidden" />}
              {metricsState === 'error' && <DataState kind="error" description="类型化指标查询失败；不显示 0 代替未知" />}
              {metricsState === 'empty' && <DataState kind="empty" title="该服务在窗口内没有指标样本" description="这是真实空结果，不是来源不可用" />}
              {metricsState === 'ready' && serviceMetrics && (
                <Table
                  size="small"
                  rowKey={(_r, index) => String(index)}
                  pagination={{ pageSize: 8 }}
                  dataSource={serviceMetrics.values}
                  columns={Object.keys(serviceMetrics.values[0] ?? {})
                    .slice(0, 6)
                    .map((k) => ({ title: k, dataIndex: k, key: k, render: (v: unknown) => (v === null || v === undefined ? '未提供' : String(v)) }))}
                />
              )}
            </PaneCard>

            <PaneCard title="原始日志（同一 scope 与时间窗）" action={<span className="muted-sm">{logs.length} 条</span>}>
              {logsState === 'idle' && <DataState kind="empty" title="未选择集群作用域" description="原始日志查询需要服务端授权的集群范围" />}
              {logsState === 'loading' && <DataState kind="loading" />}
              {logsState === 'forbidden' && <DataState kind="forbidden" />}
              {logsState === 'error' && <DataState kind="error" description="原始日志来源不可用；不显示空结果代替失败" />}
              {logsState === 'empty' && <DataState kind="empty" title="该窗口内没有日志记录" description="这是真实空结果，不是来源不可用" />}
              {logsState === 'ready' && (
                <Table
                  size="small"
                  rowKey={(_r, index) => String(index)}
                  pagination={{ pageSize: 8 }}
                  dataSource={logs}
                  columns={[
                    { title: '时间', dataIndex: 'timestamp', key: 'timestamp', width: 210, render: (v?: string) => (v ? formatTime(v) : '未提供') },
                    { title: '级别', dataIndex: 'level', key: 'level', width: 90, render: (v?: string) => v || '未提供' },
                    { title: '正文', dataIndex: 'message', key: 'message', render: (v?: string) => <span className="cell-wrap">{v || '未提供'}</span> },
                  ]}
                />
              )}
            </PaneCard>
          </div>

          <div className="path-cta">
            <Button
              type="primary"
              disabled={!selectedPathId}
              onClick={() =>
                navigate(
                  `/ai-operations?selectedPathId=${encodeURIComponent(selectedPathId ?? '')}&sourcePage=/observability`,
                )
              }
            >
              交给 AI 分析该路径
            </Button>
            <Button
              disabled={!selectedPathId}
              onClick={() => navigate(`/knowledge-graph?sourcePage=/observability&pathId=${encodeURIComponent(selectedPathId ?? '')}`)}
            >
              在知识图谱查看依赖
            </Button>
          </div>
        </>
      </BoundedDataRegion>
    </div>
  )
}

export default Observability
