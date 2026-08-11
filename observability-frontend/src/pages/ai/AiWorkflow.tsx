import React, { useEffect, useState } from 'react'
import { Table, Button, Space, Tag, Drawer, Spin, message, Segmented } from 'antd'
import { listWorkflows, runFlowAsync, listFlowRuns, resumeFlowRun } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

interface Flow { id: string; name?: string; description?: string; enabled?: boolean; nodes?: any[]; edges?: any[] }
interface Run { run_id?: string; id?: string; status?: string; created_at?: string; trigger?: any; current_node?: any }

// P0-3 修复：切换到 orchestrator 自研工作流引擎 /ai/workflows
const AiWorkflow: React.FC = () => {
  const [data, setData] = useState<Flow[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<Flow | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [runsOf, setRunsOf] = useState<Flow | null>(null)
  const [runsLoading, setRunsLoading] = useState(false)
  const [running, setRunning] = useState('')

  const load = () => {
    setLoading(true)
    listWorkflows().then((r) => {
      const d = Array.isArray(r.data) ? r.data : r.data?.flows || r.data?.data || []
      setData(d)
    }).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  // 运行：调用自研引擎 run
  const run = (f: Flow) => {
    setRunning(String(f.id))
    runFlowAsync(String(f.id), {})
      .then((r) => {
        const runId = r.data?.run_id || r.data?.id
        message.success(runId ? `已触发运行 (run: ${runId})` : '已触发运行')
      })
      .catch((e) => message.error('运行失败：' + (e?.response?.data?.detail || e?.response?.data?.error || e?.message || '')))
      .finally(() => setRunning(''))
  }

  // 运行历史
  const openRuns = (f: Flow) => {
    setRunsOf(f)
    setRuns([])
    setRunsLoading(true)
    listFlowRuns(String(f.id)).then((r) => {
      const d = Array.isArray(r.data) ? r.data : r.data?.runs || r.data?.data || []
      setRuns(d)
    }).catch(() => setRuns([])).finally(() => setRunsLoading(false))
  }

  // 审批恢复：流程中等待人工审批的节点，批准/驳回后继续
  const resume = (run: Run, approved: boolean) => {
    if (!runsOf) return
    resumeFlowRun(String(runsOf.id), String(run.run_id || run.id || ''), approved)
      .then(() => { message.success(approved ? '已批准，流程继续' : '已驳回，流程终止'); openRuns(runsOf) })
      .catch((e) => message.error('操作失败：' + (e?.response?.data?.detail || e?.response?.data?.error || e?.message || '')))
  }

  const statusColor = (s?: string) => {
    if (!s) return 'default'
    if (s === 'completed' || s === 'succeeded') return 'green'
    if (s === 'failed' || s === 'error') return 'red'
    if (s === 'waiting' || s === 'waiting_approval' || s === 'awaiting_approval' || s === 'pending_approval') return 'orange'
    if (s === 'running' || s === 'in_progress') return 'blue'
    return 'default'
  }

  const cols = [
    { title: '流程', dataIndex: 'name', key: 'name', render: (_: any, r: Flow) => r.name || r.id },
    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ color: 'var(--text-muted)', fontSize: 12 }}>{v || '-'}</span> },
    { title: '节点', dataIndex: 'nodes', key: 'nodes', width: 80, render: (v: any[], r: Flow) => v?.length ?? r.edges?.length ?? 0 },
    { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 80, render: (v: boolean) => v === false ? <Tag>停用</Tag> : <Tag color="green">启用</Tag> },
    { title: '操作', key: 'op', width: 200, render: (_: any, r: Flow) => (
        <Space size={4}>
          <Button size="small" onClick={() => setDetail(r)}>查看</Button>
          <Button size="small" type="primary" ghost loading={running === String(r.id)} disabled={r.enabled === false} onClick={() => run(r)}>运行</Button>
          <Button size="small" onClick={() => openRuns(r)}>运行历史</Button>
        </Space>
      ) },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '工作流' }]} />
      <PageHeader title="工作流" desc="AI 诊断 / 应急处置编排引擎 · 可视化 DAG · 审批门控" />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={false} locale={{ emptyText: <Empty text="暂无工作流（自研引擎）" /> }} />
      </div>

      {/* 流程详情 */}
      <Drawer width={560} open={!!detail} onClose={() => setDetail(null)} title={detail?.name || detail?.id || '工作流'}
        styles={{ body: { padding: 16 } }}>
        {detail && (
          <div>
            <p style={{ color: 'var(--text-muted)', fontSize: 13 }}>{detail.description || '暂无描述'}</p>
            <div style={{ fontWeight: 600, fontSize: 13, margin: '8px 0' }}>节点序列（{detail.nodes?.length || 0}）</div>
            {(detail.nodes || []).map((n: any, i: number) => (
              <div key={n.id || i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 0', borderBottom: '1px solid var(--border-soft)' }}>
                <span style={{ width: 18, color: 'var(--text-muted)', fontSize: 11 }}>{i + 1}</span>
                <Tag color="blue">{n.type || n.kind || 'node'}</Tag><span style={{ fontSize: 13 }}>{n.name || n.title || '-'}</span>
              </div>
            ))}
            {(detail.nodes || []).length === 0 && <Empty text="暂无节点" />}
          </div>
        )}
      </Drawer>

      {/* 运行历史 + 审批门控 */}
      <Drawer width={560} open={!!runsOf} onClose={() => setRunsOf(null)} title={'运行历史 · ' + (runsOf?.name || runsOf?.id || '')}
        styles={{ body: { padding: 16 } }}>
        {runsLoading ? <Spin /> : (runs.length === 0 ? <Empty text="暂无运行记录" /> : runs.map((run, i) => (
          <div key={i} style={{ padding: '10px 0', borderBottom: '1px solid var(--border-soft)' }}>
            <Space wrap style={{ width: '100%', justifyContent: 'space-between' }}>
              <span style={{ fontSize: 13 }}>Run {run.run_id || run.id}</span>
              <Tag color={statusColor(run.status)}>{run.status || 'unknown'}</Tag>
            </Space>
            <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 4 }}>
              {run.created_at ? new Date(String(run.created_at)).toLocaleString('zh-CN') : ''}
              {run.current_node ? ' · 当前节点: ' + (run.current_node?.name || run.current_node) : ''}
            </div>
            {(run.status === 'waiting' || run.status === 'waiting_approval' || run.status === 'awaiting_approval' || run.status === 'pending_approval') && (
              <Space style={{ marginTop: 8 }}>
                <Button size="small" type="primary" ghost onClick={() => resume(run, true)}>批准继续</Button>
                <Button size="small" danger onClick={() => resume(run, false)}>驳回终止</Button>
              </Space>
            )}
          </div>
        )))}
      </Drawer>
    </div>
  )
}

export default AiWorkflow
