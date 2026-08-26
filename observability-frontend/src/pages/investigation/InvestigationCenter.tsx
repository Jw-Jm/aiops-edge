import React, { useEffect, useMemo, useState } from 'react'
import { Badge, Button, Card, Table, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { listRuns } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const { Text } = Typography

interface InvestigationRun {
  runId: string
  tenantId: string
  clusterId: string
  resourceId: string
  symptom: string
  status: 'planning' | 'investigating' | 'awaiting_approval' | 'executing' | 'success' | 'failed' | 'cancelled'
  rootCause: string | null
  confidence: number | null
  createdBy: string
  createdAt: string
}

// P12.2：调查中心以 Run 为主对象，展示用户人工发起的调查。
// 数据源：GET /api/v1/ai/runs（真实数据源；无数据/失败显示空列表，不降级伪造 DEMO）
const statusTone: Record<string, 'default' | 'processing' | 'success' | 'warning' | 'error'> = {
  planning: 'processing', investigating: 'processing', awaiting_approval: 'warning',
  executing: 'processing', success: 'success', failed: 'error', cancelled: 'default',
}

const InvestigationCenter: React.FC = () => {
  const navigate = useNavigate()
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const [runs, setRuns] = useState<InvestigationRun[]>([])

  useEffect(() => {
    // P12：接真实 Run 数据源 GET /api/v1/ai/runs；无数据/API 失败保持空列表（不伪造 DEMO，不自动创建 Run）
    let cancelled = false
    listRuns()
      .then((resp) => {
        const list = resp.data?.runs ?? []
        if (!cancelled) {
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
        }
      })
      .catch(() => {
        if (!cancelled) setRuns([])
      })
    return () => { cancelled = true }
  }, [currentClusterId])

  const columns = useMemo(() => [
    { title: 'Run ID', dataIndex: 'runId', key: 'runId', render: (v: string) => <Text code>{v}</Text> },
    { title: '资源', dataIndex: 'resourceId', key: 'resourceId' },
    { title: '症状', dataIndex: 'symptom', key: 'symptom', ellipsis: true },
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
    {
      title: '操作', key: 'action',
      render: (_: unknown, r: InvestigationRun) => (
        <Button size="small" type="link" onClick={() => navigate(`/investigation/${r.runId}`)}>
          查看调查
        </Button>
      ),
    },
  ], [navigate])

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
        <Table<InvestigationRun> rowKey="runId" columns={columns} dataSource={runs} pagination={{ pageSize: 10 }} />
      </Card>
    </div>
  )
}

export default InvestigationCenter
