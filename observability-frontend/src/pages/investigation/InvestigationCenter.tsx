import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Badge, Button, Card, Descriptions, Drawer, Space, Table, Tabs, Tag, Typography } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { listRuns } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import ErrorState from '../../components/ErrorState'
import { useScopeStore } from '../../store/scopeStore'
import { resourceDomainOf, resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import { investigationSourceLabel, investigationStatusLabel, type InvestigationSource } from '../../features/workflow/statusPresentation'

const { Text } = Typography

interface InvestigationRun {
  runId: string
  tenantId: string
  clusterId: string
  resourceId: string
  resourceType: string
  resourceName: string
  symptom: string
  status: string
  rootCause: string | null
  confidence: number | null
  createdBy: string
  principalType: string
  source: InvestigationSource
  createdAt: string
  timeStart: string
  timeEnd: string
  evidenceCount: number | null
}

// P12.2：调查中心以 Run 为主对象，展示用户人工发起的调查。
// 数据源：GET /api/v1/ai/runs（真实数据源；无数据/失败显示空列表，不降级伪造 DEMO）
const statusTone: Record<string, 'default' | 'processing' | 'success' | 'warning' | 'error'> = {
  created: 'processing', planning: 'processing', investigating: 'processing', awaiting_confirmation: 'warning', awaiting_approval: 'warning',
  executing: 'processing', verifying: 'processing', success: 'success', partial: 'warning', failed: 'error', regressed: 'error', cancelled: 'default',
}
const queueViews = [
  { key: 'needs_action', label: '需要我处理', statuses: ['awaiting_confirmation', 'awaiting_approval', 'failed', 'regressed'] },
  { key: 'investigating', label: '正在调查', statuses: ['created', 'planning', 'investigating', 'executing'] },
  { key: 'verification', label: '待验证', statuses: ['verifying', 'partial'] },
  { key: 'ended', label: '已结束', statuses: ['success', 'cancelled'] },
]

/** 来源投影：区分人工发起、系统建议、系统自动，绝不把未知发起人写成 synthetic "system"。 */
export function projectInvestigationSource(principalType: string, actionMode: string): InvestigationSource {
  const normalized = (principalType || '').toLowerCase()
  if (normalized === 'user') return 'human'
  if (normalized === 'system') return actionMode === 'read_only' ? 'system_auto' : 'system_suggested'
  return 'unknown'
}

const InvestigationCenter: React.FC = () => {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const [runs, setRuns] = useState<InvestigationRun[]>([])
  const [selected, setSelected] = useState<InvestigationRun | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      // P12：接真实 Run 数据源 GET /api/v1/ai/runs；失败必须显式呈现，不伪造 DEMO 或健康空列表。
      if (!activeClusterId) { setRuns([]); return }
      const resp = await listRuns({ cluster_id: activeClusterId })
      const list = (resp.data?.runs ?? []).filter((run) => !run.primary_cluster_id || run.primary_cluster_id === activeClusterId)
      setRuns(list.map((r) => ({
        runId: r.run_id,
        tenantId: r.tenant_id ?? '',
        clusterId: r.primary_cluster_id ?? '',
        resourceId: r.target_resource_id ?? 'investigation',
        resourceType: r.target_type ?? '',
        resourceName: r.target_resource_id ?? '',
        symptom: r.intent ?? '—',
        status: r.status ?? 'created',
        rootCause: r.root_cause ?? null,
        confidence: r.confidence ?? null,
        createdBy: r.created_by ?? r.principal_id ?? 'unknown',
        principalType: r.principal_type ?? '',
        source: projectInvestigationSource(r.principal_type ?? '', r.action_mode ?? ''),
        createdAt: r.created_at ?? '',
        timeStart: r.query_window_start ?? '',
        timeEnd: r.query_window_end ?? '',
        evidenceCount: r.evidence_count ?? null,
      })))
    } catch (e: any) {
      setRuns([])
      setError(e?.response?.data?.error || e?.message || '调查数据加载失败')
    }
  }, [activeClusterId])

  useEffect(() => { void load() }, [load])

  // 首屏固定六列：资源、症状、状态、证据、发起时间、操作。
  // Run ID / 集群 ID / 冻结窗口 / 根因 / 置信度 / 发起人 / 快照 / 审计进入详情 Drawer。
  const columns = useMemo(() => [
    { title: '资源', key: 'resource', width: 250, render: (_: unknown, run: InvestigationRun) => {
      const domain = resourceDomainOf(run.resourceType as never)
      return !run.resourceId || run.resourceType === 'cluster' || run.resourceType === 'k8s_cluster' || !domain
        ? <Text type="secondary">集群范围</Text>
        : <Space size={4}><Tag color="blue">{resourceTypeLabel(run.resourceType)}</Tag><span className="cell-wrap">{resourceLocation({ clusterId: run.clusterId, uid: run.resourceId, type: run.resourceType, domain, name: run.resourceName || run.resourceId })}</span></Space>
    } },
    { title: '症状', dataIndex: 'symptom', key: 'symptom', render: (value: string) => <span className="cell-wrap">{value}</span> },
    { title: '状态', dataIndex: 'status', key: 'status', width: 110, render: (value: string, run: InvestigationRun) => (
      <Space direction="vertical" size={2}>
        <Badge status={statusTone[value] ?? 'default'} text={investigationStatusLabel(value)} />
        <Tag>{investigationSourceLabel(run.source)}</Tag>
      </Space>
    ) },
    { title: '证据', key: 'evidence', width: 80, responsive: ['xl'] as ('xl')[], render: (_: unknown, run: InvestigationRun) => run.evidenceCount == null ? <Text type="secondary">未提供</Text> : `${run.evidenceCount} 条` },
    { title: '发起时间', dataIndex: 'createdAt', key: 'createdAt', width: 160, responsive: ['xl'] as ('xl')[], render: (value: string) => value ? value.slice(0, 19).replace('T', ' ') : '未提供' },
    { title: '操作', key: 'action', width: 96, render: (_: unknown, r: InvestigationRun) => (
      <Button size="small" type="link" onClick={() => setSelected(r)}>查看详情</Button>
    ) },
  ], [])

  // Keep the action queue first when it has work; otherwise put an active run
  // in front so a just-created investigation is immediately discoverable.
  const defaultView = runs.some((run) => queueViews[0].statuses.includes(run.status))
    ? 'needs_action'
    : runs.some((run) => queueViews[1].statuses.includes(run.status)) ? 'investigating'
      : runs.some((run) => queueViews[2].statuses.includes(run.status)) ? 'verification' : 'ended'
  const requested = params.get('view') || defaultView
  const activeView = queueViews.some((view) => view.key === requested) ? requested : 'needs_action'

  return (
    <div>
      <PageHeader
        title="调查中心"
        desc="用户人工发起的智能调查（Run 为主对象）"
        actions={
          <Button type="primary" onClick={() => navigate('/investigation/new')}>
            发起 AI 调查
          </Button>
        }
      />
      <Card size="small">
        {error ? <ErrorState message={error} onRetry={() => { void load() }} /> : <Tabs activeKey={activeView} items={queueViews.map((view) => ({ key: view.key, label: `${view.label} (${runs.filter((run) => view.statuses.includes(run.status)).length})`, children: <Table<InvestigationRun> rowKey="runId" tableLayout="fixed" columns={columns} dataSource={runs.filter((run) => view.statuses.includes(run.status))} pagination={{ pageSize: 10 }} scroll={{ x: 'max-content' }} onRow={(run) => ({ onClick: () => setSelected(run) })} /> }))} onChange={(key) => setParams({ view: key })} destroyOnHidden />}
      </Card>
      <Drawer title="调查详情" width="min(720px, 100vw)" open={!!selected} onClose={() => setSelected(null)}>
        {selected && (
          <Descriptions size="small" column={1} bordered items={[
            { key: 'run', label: 'Run ID', children: <Text code copyable>{selected.runId}</Text> },
            { key: 'request', label: '来源', children: investigationSourceLabel(selected.source) },
            { key: 'cluster', label: '集群 ID', children: <Text code copyable>{selected.clusterId || '未提供'}</Text> },
            { key: 'tenant', label: '租户 ID', children: <Text code copyable>{selected.tenantId || '未提供'}</Text> },
            { key: 'resource', label: '目标资源', children: <Text code copyable>{selected.resourceId || '未提供'}</Text> },
            { key: 'window', label: '冻结窗口', children: selected.timeStart || selected.timeEnd ? `${selected.timeStart || '未提供'} – ${selected.timeEnd || '未提供'}` : '未提供' },
            { key: 'status', label: '状态', children: investigationStatusLabel(selected.status) },
            { key: 'root', label: '根因', children: selected.rootCause || '尚未确认' },
            { key: 'confidence', label: '置信度', children: selected.confidence == null ? '未提供' : `${Math.round(selected.confidence * 100)}%` },
            { key: 'evidence', label: '证据', children: selected.evidenceCount == null ? '未提供' : `${selected.evidenceCount} 条` },
            { key: 'creator', label: '发起人', children: <Text code copyable>{selected.createdBy}</Text> },
            { key: 'created', label: '发起时间', children: selected.createdAt || '未提供' },
          ]} />
        )}
      </Drawer>
    </div>
  )
}

export default InvestigationCenter
