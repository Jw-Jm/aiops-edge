import React, { useEffect, useState } from 'react'
import { Table, Segmented, Button, Space, message, Tag } from 'antd'
import { listApprovalTasks, approveTask, rejectTask } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

interface Task { id: string; title?: string; task_name?: string; status?: string; expert?: string; created_at?: string; service?: string; context?: string; source?: string }

const STATUS_TONE: Record<string, 'ok' | 'warn' | 'crit' | 'info' | 'muted'> = {
  waiting: 'warn', pending: 'warn', running: 'info', done: 'ok', success: 'ok', approved: 'ok', failed: 'crit', rejected: 'crit',
}

const AiTasks: React.FC = () => {
  const [tab, setTab] = useState<'all' | 'waiting' | 'done'>('all')
  const [data, setData] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)

  const load = () => {
    setLoading(true)
    listApprovalTasks({ limit: 200 }).then((r) => {
      const d = r.data?.tasks || r.data || []
      setData(d)
    }).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  // 2.12 已完成：兼容多种成功/终态状态值
  const isDone = (st?: string) => ['done', 'success', 'approved', 'rejected', 'failed'].includes(st || '')
  const filtered = tab === 'all' ? data : tab === 'done' ? data.filter((t) => isDone(t.status)) : data.filter((t) => t.status === 'waiting')

  const cols = [
    { title: '任务', dataIndex: 'context', key: 'context', render: (_: any, r: Task) => r.context || r.title || r.task_name || r.id },
    { title: '来源', dataIndex: 'source', key: 'source', render: (v: string) => v && <Tag>{v}</Tag> },
    { title: '状态', dataIndex: 'status', key: 'status', render: (v: string) => <StatusBadge text={v || 'unknown'} tone={STATUS_TONE[v] || 'muted'} /> },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
    { title: '操作', key: 'op', width: 160, render: (_: any, r: Task) => r.status === 'waiting' ? (
        <Space size={4}>
          <Button size="small" type="primary" onClick={() => approveTask(r.id).then(() => { message.success('已批准'); load() }).catch(() => message.error('失败'))}>批准</Button>
          <Button size="small" danger onClick={() => rejectTask(r.id).then(() => { message.success('已驳回'); load() }).catch(() => message.error('失败'))}>驳回</Button>
        </Space>
      ) : <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>已处理</span> },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '任务工作台' }]} />
      <PageHeader title="任务工作台" desc="AI 诊断任务与巡检报告的运行状态、审批流"
        actions={<Segmented value={tab} onChange={(v) => setTab(v as any)} options={[{ label: '全部', value: 'all' }, { label: '待审批', value: 'waiting' }, { label: '已完成', value: 'done' }]} />} />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={filtered} size="middle"
          pagination={{ pageSize: 20 }} locale={{ emptyText: <Empty text="暂无任务" /> }} />
      </div>
    </div>
  )
}

export default AiTasks
