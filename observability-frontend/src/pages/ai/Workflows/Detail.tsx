import React, { useEffect, useState } from 'react'
import { Button, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import {
  getWorkflow, listFlowRuns, getFlowRun, resumeFlowRun,
  type FlowItem, type FlowRunItem, type RunNodeItem,
  runStatusText, runStatusTone, parseRunOutput,
} from '../../../api/workflows'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../../components/ui/PageKit'
import { useAuthStore } from '../../../store/authStore'
import AppIcon from '../../../components/AppIcons'

const { Text } = Typography

// =====================================================================
//  工作流详情：运行历史表（GET /{id}/runs）
//  + 运行明细 Drawer（nodes 表格：状态/输出/错误）
//  + waiting_approval 状态行的「批准/拒绝」按钮（POST resume，仅 approver）
// =====================================================================

const WorkflowDetail: React.FC = () => {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const role = useAuthStore((s) => s.role)
  const isApprover = role === 'approver' || role === 'admin'

  const [flow, setFlow] = useState<FlowItem | null>(null)
  const [runs, setRuns] = useState<FlowRunItem[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<FlowRunItem | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [resuming, setResuming] = useState(false)

  const load = async () => {
    if (!id) return
    setLoading(true)
    try {
      const r = await getWorkflow(id)
      setFlow(r?.data?.flow || r?.data || null)
      const rr = await listFlowRuns(id)
      setRuns(rr?.data?.runs || rr?.data?.items || [])
    } catch {
      message.error('加载工作流失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [id])
  // eslint-disable-next-line react-hooks/exhaustive-deps

  // B10: 存在进行中运行（running/waiting_approval/pending 等）时每 5s 轮询刷新运行列表，
  // 终态（succeeded/failed/cancelled/skipped）后自动停止。
  useEffect(() => {
    if (!id) return
    const ACTIVE = ['running', 'waiting_approval', 'pending', 'queued', 'diagnosing']
    const hasActive = runs.some((r) => ACTIVE.includes(String(r.status || '').toLowerCase()))
    if (!hasActive) return
    const timer = setInterval(async () => {
      try {
        const rr = await listFlowRuns(id)
        setRuns(rr?.data?.runs || rr?.data?.items || [])
      } catch { /* 轮询失败忽略，下次重试 */ }
    }, 5000)
    return () => clearInterval(timer)
  }, [id, runs])

  const openDetail = async (run: FlowRunItem) => {
    if (!id) return
    setDetailLoading(true)
    setDetail(run)
    try {
      const r = await getFlowRun(id, run.run_id)
      setDetail(r?.data?.run || r?.data || run)
    } catch {
      message.error('加载运行明细失败')
    } finally {
      setDetailLoading(false)
    }
  }

  const doResume = async (runId: string, approved: boolean) => {
    if (!id) return
    setResuming(true)
    try {
      await resumeFlowRun(id, runId, approved)
      message.success(approved ? '已批准，流程继续执行' : '已拒绝')
      load()
      if (detail?.run_id === runId) openDetail({ ...detail, status: 'running' })
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '提交失败')
    } finally {
      setResuming(false)
    }
  }

  const runColumns = [
    {
      title: '运行 ID', dataIndex: 'run_id', key: 'run_id', width: 220,
      render: (v: string) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{v}</span>,
    },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 110,
      render: (v: string) => <StatusBadge text={runStatusText[v] || v} tone={runStatusTone(v)} />,
    },
    {
      title: '触发方式', dataIndex: 'trigger_type', key: 'trigger_type', width: 110,
      render: (v: string) => <Tag>{v || 'manual'}</Tag>,
    },
    {
      title: '触发时间', dataIndex: 'created_at', key: 'created_at', width: 170,
      render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{v || '—'}</span>,
    },
    {
      title: '操作', key: 'act', width: 220,
      render: (_: unknown, r: FlowRunItem) => (
        <Space wrap>
          <Button size="small" onClick={() => openDetail(r)}>运行明细</Button>
          {r.status === 'waiting_approval' && isApprover && (
            <>
              <Popconfirm title="确认拒绝？该流程将中止执行" onConfirm={() => doResume(r.run_id, false)}>
                <Button size="small" danger loading={resuming}>拒绝</Button>
              </Popconfirm>
              <Popconfirm title="确认批准该流程继续执行？环境操作将执行" onConfirm={() => doResume(r.run_id, true)}>
                <Button size="small" type="primary" loading={resuming}>批准</Button>
              </Popconfirm>
            </>
          )}
        </Space>
      ),
    },
  ]

  const nodeColumns = [
    { title: '节点 ID', dataIndex: 'node_id', key: 'node_id', width: 160 },
    { title: '类型', dataIndex: 'node_type', key: 'node_type', width: 150, render: (v: string) => <Tag>{v || '—'}</Tag> },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 110,
      render: (v: string) => <StatusBadge text={runStatusText[v] || v || '—'} tone={runStatusTone(v)} />,
    },
    {
      title: '输出', key: 'output',
      render: (_: unknown, r: RunNodeItem) => {
        const out = parseRunOutput(r)
        if (out === undefined || out === null) return <span style={{ color: 'var(--text-muted)' }}>—</span>
        const s = typeof out === 'string' ? out : JSON.stringify(out, null, 2)
        return <span style={{ fontSize: 12 }} title={s}>{String(s).slice(0, 120)}{s.length > 120 ? '…' : ''}</span>
      },
    },
    {
      title: '错误', key: 'error',
      render: (_: unknown, r: RunNodeItem) => r.error ? <span style={{ fontSize: 12, color: 'var(--danger)' }}>{String(r.error).slice(0, 120)}</span> : '—',
    },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '工作流', href: '/ai/workflows' }, { t: flow?.name || '详情' }]} />
      <PageHeader title={flow?.name || '工作流详情'} desc={flow?.description || '运行历史与节点明细'}
        actions={
          <Space wrap>
            <Button icon={<AppIcon name="workflow" />} onClick={() => id && navigate(`/ai/workflows/editor?id=${encodeURIComponent(id)}`)}>编辑</Button>
            <Button onClick={() => load()}>刷新</Button>
          </Space>
        } />

      <div className="card" style={{ marginBottom: 16, padding: 16 }}>
        <Space wrap size={24}>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>标识</Text>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 13, marginTop: 2 }}>{flow?.id || id || '—'}</div>
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>状态</Text>
            <div style={{ marginTop: 2 }}>
              {flow ? <StatusBadge text={flow.enabled === false ? '已停用' : '已启用'} tone={flow.enabled === false ? 'muted' : 'ok'} /> : '—'}
            </div>
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>节点 / 边</Text>
            <div style={{ fontSize: 13, marginTop: 2 }}>{flow?.graph?.nodes?.length ?? 0} / {flow?.graph?.edges?.length ?? 0}</div>
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>运行次数</Text>
            <div style={{ fontSize: 13, marginTop: 2 }}>{runs.length}</div>
          </div>
        </Space>
      </div>

      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="run_id" loading={loading} columns={runColumns} dataSource={runs} size="middle"
          pagination={{ pageSize: 10, showSizeChanger: false }}
          locale={{ emptyText: <Empty text="暂无运行记录" hint="在工作流列表或编辑器中手动运行，或配置 cron/告警触发器" /> }} />
      </div>

      {/* 运行明细 Drawer */}
      <Drawer title={`运行明细：${detail?.run_id || ''}`} open={!!detail} onClose={() => setDetail(null)} width={760}
        styles={{ body: { padding: 16 } }}
        extra={
          detail?.status === 'waiting_approval' && isApprover ? (
            <Space>
              <Popconfirm title="确认拒绝？该流程将中止执行" onConfirm={() => detail && doResume(detail.run_id, false)}>
                <Button size="small" danger loading={resuming}>拒绝</Button>
              </Popconfirm>
              <Popconfirm title="确认批准该流程继续执行？环境操作将执行" onConfirm={() => detail && doResume(detail.run_id, true)}>
                <Button size="small" type="primary" loading={resuming}>批准</Button>
              </Popconfirm>
            </Space>
          ) : undefined
        }>
        {detail && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <Space wrap size={16}>
              <StatusBadge text={runStatusText[detail.status] || detail.status || '—'} tone={runStatusTone(detail.status)} />
              <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>触发：{detail.trigger_type || 'manual'}</span>
              <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>时间：{detail.created_at || '—'}</span>
              {detail.error && <span style={{ fontSize: 12, color: 'var(--danger)' }}>错误：{String(detail.error).slice(0, 200)}</span>}
            </Space>
            <Table rowKey="node_id" loading={detailLoading} columns={nodeColumns} dataSource={detail?.nodes || []} size="small"
              pagination={false} locale={{ emptyText: <Empty text="暂无节点明细" /> }} />
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default WorkflowDetail
