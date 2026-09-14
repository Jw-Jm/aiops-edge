import React, { useCallback, useEffect, useMemo } from 'react'
import { Button, Space, Table, Tabs, Tag, Typography } from 'antd'
const { Text: CellText } = Typography
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { acceptAlertInvestigation, getAlertAggregation, type AlertAggregationItem, type AlertInvestigationLink } from '../../api/client'
import type { ProblemSummary } from '../../lib/readModels'
import { Breadcrumb, PageHeader, StatusBadge } from '../../components/ui/PageKit'
import AlertEvents from '../alerts/AlertEvents'
import Trace from '../observability/Trace'
import LogMetrics from '../observability/LogMetrics'
import Changes from '../infra/Changes'
import { useScopeStore } from '../../store/scopeStore'
import { queryKeys } from '../../query/keys'
import DataState from '../../components/display/DataState'
import { resourceLocation, resourceTypeLabel, resourceDomainOf } from '../../features/resources/resourceDomain'
import { alertInvestigationView, ALERT_INVESTIGATION_READONLY_NOTICE } from '../../features/workflow/alertInvestigation'
import type { PlatformResourceRef } from '../../features/resources/types'

/** 聚合事件可能携带服务端注入的调查投影。 */
interface AlertInvestigationLinkCarrier {
  investigation_link?: AlertInvestigationLink
}

function aggregationResource(item: AlertAggregationItem, clusterId: string): PlatformResourceRef | undefined {
  const uid = item.resource_uid || item.service
  if (!uid) return undefined
  const type = item.resource_type || 'service'
  return { clusterId: item.cluster_id || clusterId, uid, type: type as never, domain: resourceDomainOf(type as never) || 'application', name: item.resource_name || item.service || uid }
}

export function toProblem(item: AlertAggregationItem, clusterId: string): ProblemSummary {
  const bySeverity = item.by_severity || {}
  const critical = Number(bySeverity.critical ?? bySeverity.严重 ?? 0)
  const warning = Number(bySeverity.warning ?? bySeverity.警告 ?? 0)
  const resource = aggregationResource(item, clusterId)
  const raw = item as AlertAggregationItem & Record<string, unknown>
  // Task 10：取聚合事件里第一条携带的调查投影，供首屏显示受控调查状态。
  const linkedEvent = (item.events ?? []).find((event) => (event as AlertInvestigationLinkCarrier).investigation_link)
  const investigation = (linkedEvent as AlertInvestigationLinkCarrier | undefined)?.investigation_link ?? null
  const numeric = (key: string): number | null => {
    const value = raw[key]
    return typeof value === 'number' && Number.isFinite(value) ? value : null
  }
  return {
    problem_id: String(item.service || 'cluster') + ':' + (item.latest_rule || 'alert'),
    title: item.latest_rule || (resource?.name || '集群') + ' 告警聚合',
    severity: critical > 0 ? 'critical' : warning > 0 ? 'warning' : 'info',
    health: critical > 0 ? 'abnormal' : warning > 0 ? 'degraded' : 'healthy',
    source_refs: item.events?.map((event) => String(event.id)) ?? [],
    primary_resource: item.service,
    resource,
    cluster_id: resource?.clusterId || clusterId,
    affected_resources: resource ? [resource.uid] : [],
    started_at: item.latest_time,
    duration_seconds: item.events?.length ? Math.max(...item.events.map((event) => {
      const start = event.first_timestamp ? Date.parse(event.first_timestamp) : NaN
      const end = event.last_timestamp ? Date.parse(event.last_timestamp) : NaN
      return Number.isFinite(start) && Number.isFinite(end) && end >= start ? (end - start) / 1000 : 0
    })) : null,
    evidence_summary: [(resource?.name || '集群范围') + ' 近期开启 ' + item.total + ' 次告警事件'],
    recent_change: item.recent_change ? '近期发生变更' : null,
    data_status: item.data_status || 'available',
    failure_rate: numeric('failure_rate') ?? numeric('error_rate'),
    ready_replicas: numeric('ready_replicas'),
    desired_replicas: numeric('desired_replicas'),
    investigation,
  }
}

const ProblemsView: React.FC = () => {
  const navigate = useNavigate()
  const tenantId = useScopeStore((state) => state.authScope?.tenantId ?? state.active?.tenantId ?? '')
  const activeScope = useScopeStore((state) => state.active)
  const activeClusterId = useScopeStore((state) => state.active?.clusterId ?? state.authScope?.activeClusterId ?? '')
  const clusters = useScopeStore((state) => state.clusters) ?? []
  const clusterName = useCallback((clusterId: string) => clusters.find((cluster) => cluster.cluster_id === clusterId)?.name ?? '', [clusters])
  const scopeResource = activeScope?.resource
  const from = activeScope?.timeRange.mode === 'absolute' ? activeScope.timeRange.start : undefined
  const to = activeScope?.timeRange.mode === 'absolute' ? activeScope.timeRange.end : undefined
  const problemsQuery = useQuery({
    queryKey: queryKeys.problems({ tenantId, activeClusterId, entityUid: scopeResource?.uid, from, to }),
    queryFn: () => getAlertAggregation({ limit: 200, cluster_id: activeClusterId, ...(scopeResource ? { resource_uid: scopeResource.uid, resource_type: scopeResource.type } : {}) }).then((response) => (response.data?.data ?? []).map((item) => toProblem(item, activeClusterId))),
    enabled: Boolean(activeClusterId),
  })
  const rows = problemsQuery.data ?? []
  const error = problemsQuery.error instanceof Error ? problemsQuery.error.message : ''
  const columns = useMemo(() => [
    { title: '问题', dataIndex: 'title', key: 'title', render: (value: string, row: ProblemSummary) => <Button type="link" onClick={() => navigate('/investigation/new?source=alert&problem_id=' + encodeURIComponent(row.problem_id) + '&resource=' + encodeURIComponent(row.resource?.uid || '') + '&symptom=' + encodeURIComponent(value))}>{value}</Button> },
    { title: '资源', key: 'resource', render: (_: unknown, row: ProblemSummary) => row.resource ? resourceTypeLabel(row.resource.type) + ' · ' + resourceLocation(row.resource) : '集群范围' },
    // 集群列在 <xl 视口收起：1024 工作区本身已绑定单一集群，宽列会挤压问题/症状。
    { title: '集群', key: 'cluster_id', width: 160, responsive: ['xl'] as ('xl')[], render: (_: unknown, row: ProblemSummary) => {
      // 显示集群名而非 36 位 UUID：UUID 可复制，不得逐字换行。
      const clusterId = row.cluster_id || ''
      const name = clusterName(clusterId)
      return name
        ? <span title={clusterId} style={{ overflowWrap: 'anywhere' }}>{name}</span>
        : <CellText code copyable={{ text: clusterId }} style={{ fontSize: 12 }}>{clusterId.slice(0, 8)}…</CellText>
    } },
    { title: '严重度', dataIndex: 'severity', key: 'severity', render: (value: string) => <StatusBadge text={value === 'critical' ? '严重' : value === 'warning' ? '警告' : '信息'} tone={value === 'critical' ? 'crit' : value === 'warning' ? 'warn' : 'info'} /> },
    { title: '健康', dataIndex: 'health', key: 'health', render: (value: string) => <Tag color={value === 'abnormal' ? 'red' : value === 'degraded' ? 'orange' : 'green'}>{value === 'abnormal' ? '异常' : value === 'degraded' ? '降级' : '健康'}</Tag> },
    { title: '影响', key: 'count', render: (_: unknown, row: ProblemSummary) => row.affected_resources.length || '—' },
    { title: '最近发生', dataIndex: 'started_at', key: 'started_at' },
    { title: '数据状态', dataIndex: 'data_status', key: 'data_status', render: (value: string) => <Tag color={value === 'available' ? 'green' : 'orange'}>{value === 'available' ? '数据正常' : value}</Tag> },
    // Task 10：告警调查状态与主动作必须一致；skipped 原因用中文说明且不隐藏。
    { title: '调查', key: 'investigation', width: 200, render: (_: unknown, row: ProblemSummary) => {
      const view = alertInvestigationView(row.investigation)
      const eventId = row.source_refs?.[0]
      const run = () => {
        if (!eventId) return
        void acceptAlertInvestigation(eventId).then(() => problemsQuery.refetch()).catch(() => undefined)
      }
      const action = view.action === 'create' || view.action === 'start'
        ? <Button size="small" type="link" onClick={run}>{view.action === 'start' ? '开始调查' : '发起调查'}</Button>
        : view.action === 'open'
          ? <Button size="small" type="link" onClick={() => navigate(`/investigation/${row.investigation?.run_id ?? ''}`)}>打开调查</Button>
          : view.action === 'view'
            ? <Button size="small" type="link" onClick={() => navigate(`/investigation/${row.investigation?.run_id ?? ''}`)}>查看结论</Button>
            : null
      return (
        <Space direction="vertical" size={0}>
          <Space size={4}><Tag color={view.tone === 'processing' ? 'blue' : view.tone === 'success' ? 'green' : view.tone === 'warning' ? 'gold' : 'default'}>{view.label}</Tag>{action}</Space>
          {view.reason && <Typography.Text type="secondary" style={{ fontSize: 11 }}>{view.reason}</Typography.Text>}
        </Space>
      )
    } },
  ], [navigate, clusterName, problemsQuery])

  const scopeTag = scopeResource ? (
    <Tag color="blue" style={{ marginBottom: 12 }}>资源筛选：{resourceTypeLabel(scopeResource.type)} · {resourceLocation(scopeResource)}</Tag>
  ) : null

  // loading / error / empty / ready 必须互斥：接口失败时不得同时显示健康空态或事实条。
  if (!activeClusterId) {
    return <DataState kind="empty" title="请选择集群" description="选择集群后查看问题" />
  }
  if (problemsQuery.isLoading) {
    return <DataState kind="loading" title="正在读取问题" />
  }
  if (problemsQuery.isError) {
    return <DataState kind="error" title="问题数据读取失败" description={error || '告警聚合暂不可用'} onRetry={() => void problemsQuery.refetch()} />
  }
  if (rows.length === 0) {
    return <DataState kind="empty" title="暂无问题" description="当前集群没有可展示的活动问题" />
  }

  const leading = rows[0]
  return (
    <div>
      {scopeTag}
      <div className="observe-fact-strip" aria-label="问题事实摘要">
        <div><span>失败率</span><strong>{leading?.failure_rate == null ? '未提供' : `${(leading.failure_rate * 100).toFixed(1)}%`}</strong></div>
        <div><span>就绪副本</span><strong>{leading?.ready_replicas == null ? '未提供' : `${leading.ready_replicas}/${leading.desired_replicas ?? '—'}`}</strong></div>
      </div>
      <Table rowKey="problem_id" columns={columns} dataSource={rows} pagination={{ pageSize: 20 }} />
      <Typography.Text type="secondary" style={{ display: "block", marginTop: 8, fontSize: 12 }}>{ALERT_INVESTIGATION_READONLY_NOTICE}</Typography.Text>
    </div>
  )
}

const Observe: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const requested = params.get('view') || 'problems'
  const items = [
    { key: 'problems', label: '问题', children: <ProblemsView /> },
    { key: 'alerts', label: '告警', children: <AlertEvents /> },
    { key: 'metrics', label: '指标', children: <LogMetrics /> },
    { key: 'logs', label: '日志', children: <LogMetrics /> },
    { key: 'traces', label: 'Trace', children: <Trace /> },
    { key: 'events', label: '事件', children: <AlertEvents /> },
    { key: 'changes', label: '变更', children: <Changes /> },
  ]
  const knownView = items.some((item) => item.key === requested)
  const activeKey = knownView ? requested : 'problems'
  useEffect(() => {
    if (!knownView) setParams({ view: 'problems' }, { replace: true })
  }, [knownView, setParams])
  return (
    <div>
      <Breadcrumb items={[{ t: '观测' }, { t: '问题与信号' }]} />
      <PageHeader title="观测中心" desc="先看资源问题，再下钻告警、指标、日志、Trace、事件与变更证据" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default Observe
