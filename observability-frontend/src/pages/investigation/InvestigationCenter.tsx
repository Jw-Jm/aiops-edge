import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Badge, Button, Card, Table, Tabs, Typography } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { listRuns } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import ErrorState from '../../components/ErrorState'
import { useScopeStore } from '../../store/scopeStore'

const { Text } = Typography

interface InvestigationRun {
  runId: string
  tenantId: string
  clusterId: string
  resourceId: string
  symptom: string
  status: 'created' | 'planning' | 'investigating' | 'awaiting_confirmation' | 'awaiting_approval' | 'executing' | 'verifying' | 'success' | 'partial' | 'failed' | 'regressed' | 'cancelled'
  rootCause: string | null
  confidence: number | null
  createdBy: string
  createdAt: string
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

const InvestigationCenter: React.FC = () => {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const [runs, setRuns] = useState<InvestigationRun[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      // P12：接真实 Run 数据源 GET /api/v1/ai/runs；失败必须显式呈现，不伪造 DEMO 或健康空列表。
      if (!activeClusterId) { setRuns([]); return }
      const resp = await listRuns({ cluster_id: activeClusterId })
      const list = resp.data?.runs ?? []
      setRuns(list.map((r) => ({
        runId: r.run_id,
        tenantId: r.tenant_id ?? '',
        clusterId: r.primary_cluster_id ?? '',
        resourceId: r.target_resource_id ?? 'investigation',
        symptom: r.intent ?? '—',
        status: (r.status ?? 'created') as InvestigationRun['status'],
        rootCause: r.root_cause ?? null,
        confidence: r.confidence ?? null,
        // The server projects the persisted initiating principal. Keep a
        // visible unknown state if old rows lack it; never label every run
        // as a synthetic system action.
        createdBy: r.created_by ?? r.principal_id ?? 'unknown',
        createdAt: r.created_at ?? '',
      })))
    } catch (e: any) {
      setRuns([])
      setError(e?.response?.data?.error || e?.message || '调查数据加载失败')
    }
  }, [activeClusterId])

  useEffect(() => { void load() }, [load])

  const columns = useMemo(() => [
    { title: '资源', dataIndex: 'resourceId', key: 'resourceId' },
    { title: '症状', dataIndex: 'symptom', key: 'symptom', ellipsis: true },
    { title: '影响', key: 'impact', render: () => <Text type="secondary">未提供</Text> },
    { title: '持续时间', key: 'duration', render: () => <Text type="secondary">未提供</Text> },
    {
      title: '状态', dataIndex: 'status', key: 'status',
      render: (v: InvestigationRun['status']) => (
        <Badge status={statusTone[v] ?? 'default'} text={v.replace('_', ' ')} />
      ),
    },
    { title: '根因', dataIndex: 'rootCause', key: 'rootCause', render: (v: string | null) => v ?? <Text type="secondary">—</Text> },
    { title: '置信度', dataIndex: 'confidence', key: 'confidence', render: (v: number | null) => v == null ? <Text type="secondary">—</Text> : `${(v * 100).toFixed(0)}%` },
    { title: '发起人', dataIndex: 'createdBy', key: 'createdBy' },
    { title: '发起时间', dataIndex: 'createdAt', key: 'createdAt' },
    { title: 'Run ID', dataIndex: 'runId', key: 'runId', render: (v: string) => <Text code>{v}</Text> },
    {
      title: '操作', key: 'action',
      render: (_: unknown, r: InvestigationRun) => (
        <Button size="small" type="link" onClick={() => navigate(`/investigation/${r.runId}`)}>
          查看调查
        </Button>
      ),
    },
  ], [navigate])

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
        {error ? <ErrorState message={error} onRetry={() => { void load() }} /> : <Tabs activeKey={activeView} items={queueViews.map((view) => ({ key: view.key, label: `${view.label} (${runs.filter((run) => view.statuses.includes(run.status)).length})`, children: <Table<InvestigationRun> rowKey="runId" columns={columns} dataSource={runs.filter((run) => view.statuses.includes(run.status))} pagination={{ pageSize: 10 }} /> }))} onChange={(key) => setParams({ view: key })} destroyOnHidden />}
      </Card>
    </div>
  )
}

export default InvestigationCenter
