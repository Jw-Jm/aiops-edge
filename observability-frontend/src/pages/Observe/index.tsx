import React, { useMemo } from 'react'
import { Alert, Button, Empty as AntEmpty, Table, Tabs, Tag } from 'antd'
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
import { useScopeStore } from '../../store/scopeStore'
import { queryKeys } from '../../query/keys'

function toProblem(item: AlertAggregationItem): ProblemSummary {
  const bySeverity = item.by_severity || {}
  const critical = Number(bySeverity.critical ?? bySeverity.严重 ?? 0)
  const warning = Number(bySeverity.warning ?? bySeverity.警告 ?? 0)
  return {
    problem_id: `${item.service}:${item.latest_rule || 'alert'}`,
    title: item.latest_rule || `${item.service} 告警聚合`,
    severity: critical > 0 ? 'critical' : warning > 0 ? 'warning' : 'info',
    health: critical > 0 ? 'abnormal' : warning > 0 ? 'degraded' : 'healthy',
    source_refs: item.events?.map((event) => String(event.id)) ?? [],
    primary_resource: item.service,
    affected_resources: [item.service],
    started_at: item.latest_time,
    duration_seconds: null,
    evidence_summary: [`${item.service} 近期开启 ${item.total} 次告警事件`],
    recent_change: null,
    data_status: 'available',
  }
}

const ProblemsView: React.FC = () => {
  const navigate = useNavigate()
  const tenantId = useScopeStore((state) => state.authScope?.tenantId ?? '')
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const problemsQuery = useQuery({
    queryKey: queryKeys.problems({ tenantId, activeClusterId }),
    queryFn: () => getAlertAggregation({ limit: 200 }).then((response) => (response.data?.data ?? []).map(toProblem)),
    enabled: Boolean(activeClusterId),
  })
  const rows = problemsQuery.data ?? []
  const loading = problemsQuery.isLoading
  const error = problemsQuery.error instanceof Error ? problemsQuery.error.message : ''

  const columns = useMemo(() => [
    { title: '问题', dataIndex: 'title', key: 'title', render: (value: string, row: ProblemSummary) => <Button type="link" onClick={() => navigate(`/investigation/new?problem_id=${encodeURIComponent(row.problem_id)}&service=${encodeURIComponent(row.primary_resource || '')}`)}>{value}</Button> },
    { title: '服务', dataIndex: 'primary_resource', key: 'primary_resource' },
    { title: '严重度', dataIndex: 'severity', key: 'severity', render: (value: string) => <StatusBadge text={value === 'critical' ? '严重' : value === 'warning' ? '警告' : '信息'} tone={value === 'critical' ? 'crit' : value === 'warning' ? 'warn' : 'info'} /> },
    { title: '健康', dataIndex: 'health', key: 'health', render: (value: string) => <Tag color={value === 'abnormal' ? 'red' : value === 'degraded' ? 'orange' : 'green'}>{value === 'abnormal' ? '异常' : value === 'degraded' ? '降级' : '健康'}</Tag> },
    { title: '次数', key: 'count', render: (_: unknown, row: ProblemSummary) => row.evidence_summary[0]?.match(/\d+/)?.[0] ?? '—' },
    { title: '最近发生', dataIndex: 'started_at', key: 'started_at' },
    { title: '数据状态', dataIndex: 'data_status', key: 'data_status', render: (value: string) => <Tag color={value === 'available' ? 'green' : 'orange'}>{value === 'available' ? '数据正常' : value}</Tag> },
  ], [navigate])

  if (!activeClusterId) return <AntEmpty description="请选择作用域后查看问题" />
  return (
    <div>
      {error ? <Alert type="error" showIcon message={error} action={<Button size="small" onClick={() => void problemsQuery.refetch()}>重试</Button>} style={{ marginBottom: 12 }} /> : null}
      <Table rowKey="id" loading={loading} columns={columns} dataSource={rows} pagination={{ pageSize: 20 }} locale={{ emptyText: <AntEmpty description="暂无问题" /> }} />
    </div>
  )
}

const Observe: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const requested = params.get('view') || 'problems'
  const items = [
    { key: 'problems', label: '问题', children: <ProblemsView /> },
    { key: 'alerts', label: '原始告警', children: <AlertEvents /> },
    { key: 'traces', label: '调用链', children: <Trace /> },
    { key: 'telemetry', label: '日志与指标', children: <LogMetrics /> },
    { key: 'changes', label: '变更', children: <Changes /> },
    { key: 'grafana', label: 'Grafana', children: <Grafana /> },
  ]
  const activeKey = items.some((item) => item.key === requested) ? requested : 'problems'
  return (
    <div>
      <Breadcrumb items={[{ t: '观测' }, { t: '问题与信号' }]} />
      <PageHeader title="观测中心" desc="先看问题，再下钻原始告警、调用链、日志与变更证据" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default Observe
