import React, { useEffect, useState } from 'react'
import { Table, Segmented, Button, Space, message, Tag, Drawer, Descriptions } from 'antd'
import { listApprovalTasks, approveTask, rejectTask } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

interface Task { id: string; title?: string; task_name?: string; status?: string; expert?: string; created_at?: string; service?: string; context?: string; source?: string; diagnosis?: string; plan?: string; script?: string; report?: string; done_at?: string }

const STATUS_TONE: Record<string, 'ok' | 'warn' | 'crit' | 'info' | 'muted'> = {
  waiting: 'warn', pending: 'warn', running: 'info', done: 'ok', success: 'ok', approved: 'ok', failed: 'crit', rejected: 'crit',
}

const AiTasks: React.FC = () => {
  const [tab, setTab] = useState<'all' | 'waiting' | 'done'>('all')
  const [data, setData] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<Task | null>(null)

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

  // Issue1: 每项任务提供详情抽屉（含诊断、方案、脚本、结果等完整信息）
  const cols = [
    { title: '任务', dataIndex: 'context', key: 'context', render: (_: any, r: Task) => r.context || r.title || r.task_name || r.id },
    { title: '服务', dataIndex: 'service', key: 'service', render: (v: string) => v ? <Tag>{v}</Tag> : '-' },
    { title: '来源', dataIndex: 'source', key: 'source', render: (v: string) => v && <Tag>{v}</Tag> },
    { title: '状态', dataIndex: 'status', key: 'status', render: (v: string) => <StatusBadge text={v || 'unknown'} tone={STATUS_TONE[v] || 'muted'} /> },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
    { title: '操作', key: 'op', width: 210, render: (_: any, r: Task) => (
        <Space size={4}>
          <Button size="small" onClick={() => setDetail(r)}>详情</Button>
          {r.status === 'waiting' ? (
            <><Button size="small" type="primary" onClick={() => approveTask(r.id).then(() => { message.success('已批准'); load() }).catch(() => message.error('失败'))}>批准</Button>
              <Button size="small" danger onClick={() => rejectTask(r.id).then(() => { message.success('已驳回'); load() }).catch(() => message.error('失败'))}>驳回</Button></>
          ) : <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>已处理</span>}
        </Space>
      ) },
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

      {/* Issue1: 任务详情抽屉 */}
      <Drawer open={!!detail} onClose={() => setDetail(null)} title="任务详情" width={560}
        styles={{ body: { padding: 20, background: 'var(--surface-1)' } }}>
        {detail && (
          <Descriptions column={1} size="small" labelStyle={{ width: 96, color: 'var(--text-muted)' }}>
            <Descriptions.Item label="任务">{detail.context || detail.title || detail.id}</Descriptions.Item>
            <Descriptions.Item label="ID">{detail.id}</Descriptions.Item>
            <Descriptions.Item label="服务">{detail.service ? <Tag>{detail.service}</Tag> : '-'}</Descriptions.Item>
            <Descriptions.Item label="来源">{detail.source || '-'}</Descriptions.Item>
            <Descriptions.Item label="状态"><StatusBadge text={detail.status || 'unknown'} tone={STATUS_TONE[detail.status || ''] || 'muted'} /></Descriptions.Item>
            <Descriptions.Item label="创建时间">{detail.created_at || '-'}</Descriptions.Item>
            {detail.done_at && <Descriptions.Item label="完成时间">{detail.done_at}</Descriptions.Item>}
            {detail.expert && <Descriptions.Item label="审批人">{detail.expert}</Descriptions.Item>}
            {detail.diagnosis && <Descriptions.Item label="诊断"><pre style={{ whiteSpace: 'pre-wrap', margin: 0, fontSize: 12, lineHeight: 1.7 }}>{detail.diagnosis}</pre></Descriptions.Item>}
            {detail.plan && <Descriptions.Item label="方案"><pre style={{ whiteSpace: 'pre-wrap', margin: 0, fontSize: 12, lineHeight: 1.7 }}>{detail.plan}</pre></Descriptions.Item>}
            {detail.script && <Descriptions.Item label="执行脚本"><pre style={{ whiteSpace: 'pre-wrap', margin: 0, fontSize: 12, fontFamily: 'var(--font-mono)', background: 'var(--surface-2)', padding: 8, borderRadius: 6 }}>{detail.script}</pre></Descriptions.Item>}
            {detail.report && <Descriptions.Item label="结果"><pre style={{ whiteSpace: 'pre-wrap', margin: 0, fontSize: 12, lineHeight: 1.7 }}>{detail.report}</pre></Descriptions.Item>}
          </Descriptions>
        )}
      </Drawer>
    </div>
  )
}

export default AiTasks
