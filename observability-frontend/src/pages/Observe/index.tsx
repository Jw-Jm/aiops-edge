import React, { useEffect, useMemo } from 'react'
import { Alert, Button, Table, Tabs, Tag } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getAlertAggregation, type AlertAggregationItem } from '../../api/client'
import type { ProblemSummary } from '../../lib/readModels'
import { Breadcrumb, PageHeader, StatusBadge } from '../../components/ui/PageKit'
import AlertEvents from '../alerts/AlertEvents'
import Trace from '../observability/Trace'
import LogMetrics from '../observability/LogMetrics'
import Changes from '../infra/Changes'
import Grafana from '../observability/Grafana'
import AlertRules from '../alerts/AlertRules'
import { useScopeStore } from '../../store/scopeStore'
import { queryKeys } from '../../query/keys'
import DataState from '../../components/display/DataState'
import { resourceLocation, resourceTypeLabel, resourceDomainOf } from '../../features/resources/resourceDomain'
import type { PlatformResourceRef } from '../../features/resources/types'

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
  }
}

const ProblemsView: React.FC = () => {
  const navigate = useNavigate()
  const tenantId = useScopeStore((state) => state.authScope?.tenantId ?? state.active?.tenantId ?? '')
  const activeScope = useScopeStore((state) => state.active)
  const activeClusterId = useScopeStore((state) => state.active?.clusterId ?? state.authScope?.activeClusterId ?? '')
  const scopeResource = activeScope?.resource
  const from = activeScope?.timeRange.mode === 'absolute' ? activeScope.timeRange.start : undefined
  const to = activeScope?.timeRange.mode === 'absolute' ? activeScope.timeRange.end : undefined
  const problemsQuery = useQuery({
    queryKey: queryKeys.problems({ tenantId, activeClusterId, entityUid: scopeResource?.uid, from, to }),
    queryFn: () => getAlertAggregation({ limit: 200, cluster_id: activeClusterId, ...(scopeResource ? { resource_uid: scopeResource.uid, resource_type: scopeResource.type } : {}) }).then((response) => (response.data?.data ?? []).map((item) => toProblem(item, activeClusterId))),
    enabled: Boolean(activeClusterId),
  })
  const rows = problemsQuery.data ?? []
  const loading = problemsQuery.isLoading
  const error = problemsQuery.error instanceof Error ? problemsQuery.error.message : ''
  const columns = useMemo(() => [
    { title: '问题', dataIndex: 'title', key: 'title', render: (value: string, row: ProblemSummary) => <Button type="link" onClick={() => navigate('/investigation/new?source=alert&problem_id=' + encodeURIComponent(row.problem_id) + '&resource=' + encodeURIComponent(row.resource?.uid || '') + '&symptom=' + encodeURIComponent(value))}>{value}</Button> },
    { title: '资源', key: 'resource', render: (_: unknown, row: ProblemSummary) => row.resource ? resourceTypeLabel(row.resource.type) + ' · ' + resourceLocation(row.resource) : '集群范围' },
    { title: '集群', dataIndex: 'cluster_id', key: 'cluster_id' },
    { title: '严重度', dataIndex: 'severity', key: 'severity', render: (value: string) => <StatusBadge text={value === 'critical' ? '严重' : value === 'warning' ? '警告' : '信息'} tone={value === 'critical' ? 'crit' : value === 'warning' ? 'warn' : 'info'} /> },
    { title: '健康', dataIndex: 'health', key: 'health', render: (value: string) => <Tag color={value === 'abnormal' ? 'red' : value === 'degraded' ? 'orange' : 'green'}>{value === 'abnormal' ? '异常' : value === 'degraded' ? '降级' : '健康'}</Tag> },
    { title: '影响', key: 'count', render: (_: unknown, row: ProblemSummary) => row.affected_resources.length || '—' },
    { title: '最近发生', dataIndex: 'started_at', key: 'started_at' },
    { title: '数据状态', dataIndex: 'data_status', key: 'data_status', render: (value: string) => <Tag color={value === 'available' ? 'green' : 'orange'}>{value === 'available' ? '数据正常' : value}</Tag> },
  ], [navigate])

  if (!activeClusterId) return <DataState kind="empty" title="请选择集群" description="选择生产集群后查看问题" />
  return (
    <div>
      {scopeResource && <Tag color="blue" style={{ marginBottom: 12 }}>资源筛选：{resourceTypeLabel(scopeResource.type)} · {resourceLocation(scopeResource)}</Tag>}
      {error ? <Alert type="error" showIcon message={error} action={<Button size="small" onClick={() => void problemsQuery.refetch()}>重试</Button>} style={{ marginBottom: 12 }} /> : null}
      <Table rowKey="problem_id" loading={loading} columns={columns} dataSource={rows} pagination={{ pageSize: 20 }} locale={{ emptyText: <DataState kind="empty" compact title="暂无问题" /> }} />
    </div>
  )
}

const Observe: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const requested = params.get('view') || 'problems'
  const items = [
    { key: 'problems', label: '问题', children: <ProblemsView /> },
    { key: 'alerts', label: '原始告警', children: <AlertEvents /> },
    { key: 'rules', label: '告警规则', children: <AlertRules /> },
    { key: 'traces', label: '调用链', children: <Trace /> },
    { key: 'telemetry', label: '日志与指标', children: <LogMetrics /> },
    { key: 'changes', label: '变更', children: <Changes /> },
    { key: 'grafana', label: 'Grafana', children: <Grafana /> },
  ]
  const knownView = items.some((item) => item.key === requested)
  const activeKey = knownView ? requested : 'problems'
  useEffect(() => {
    if (!knownView) setParams({ view: 'problems' }, { replace: true })
  }, [knownView, setParams])
  return (
    <div>
      <Breadcrumb items={[{ t: '观测' }, { t: '问题与信号' }]} />
      <PageHeader title="观测中心" desc="先看问题，再下钻原始告警、调用链、日志与变更证据" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default Observe
