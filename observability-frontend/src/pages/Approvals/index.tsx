import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Button, Space, Typography, message, Input, Select, Popconfirm } from 'antd'
import { CheckOutlined, CloseOutlined, ReloadOutlined } from '@ant-design/icons'
import api from '../../api/client'

const { Text } = Typography

interface Task {
  id: string; status: string; source: string; service: string
  context: string; diagnosis: string; plan: string; script: string
  risk_score: number; risk_reason: string; created_at: string; done_at: string
}

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  waiting:   { color: 'warning', label: '待审批' },
  approved:  { color: 'cyan', label: '已批准' },
  running:   { color: 'processing', label: '执行中' },
  done:      { color: 'green', label: '已完成' },
  failed:    { color: 'red', label: '失败' },
  rejected:  { color: 'default', label: '已拒绝' },
  diagnosing:{ color: 'processing', label: '诊断中' },
  queued:    { color: 'default', label: '待诊断' },
}

const Approvals: React.FC = () => {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(false)
  const [statusFilter, setStatusFilter] = useState('')
  const [search, setSearch] = useState('')

  const fetchTasks = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string> = {}
      if (statusFilter) params.status = statusFilter
      const r = await api.get('/ops/tasks', { params })
      setTasks(r.data?.tasks || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [statusFilter])

  useEffect(() => { fetchTasks() }, [fetchTasks])

  const doDecide = async (tid: string, approved: boolean) => {
    try {
      await api.post(`/ops/tasks/${tid}/${approved ? 'approve' : 'reject'}`)
      message.success(approved ? '已批准' : '已驳回')
      fetchTasks()
    } catch (e: any) {
      message.error(e?.response?.data?.detail || '操作失败')
    }
  }

  const columns = [
    { title: '任务ID', dataIndex: 'id', key: 'id', width: 220, ellipsis: true },
    { title: '服务', dataIndex: 'service', key: 'service', width: 160,
      render: (v: string) => v ? <Tag color="blue">{v}</Tag> : '-' },
    { title: '风险', dataIndex: 'risk_score', key: 'risk_score', width: 90,
      render: (v: number) => {
        const n = Number(v || 0)
        if (n <= 0) return <Text type="secondary">—</Text>
        const color = n >= 70 ? 'red' : n >= 40 ? 'orange' : 'green'
        const label = n >= 70 ? '高' : n >= 40 ? '中' : '低'
        return <Tag color={color}>{label}（{Math.round(n)}）</Tag>
      } },
    { title: '状态', dataIndex: 'status', key: 'status', width: 110,
      render: (v: string) => STATUS_MAP[v] ? <Tag color={STATUS_MAP[v].color}>{STATUS_MAP[v].label}</Tag> : v },
    { title: '上下文', dataIndex: 'context', key: 'context', ellipsis: true },
    { title: '诊断', dataIndex: 'diagnosis', key: 'diagnosis', ellipsis: true },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 180 },
    { title: '操作', key: 'action', width: 160, fixed: 'right' as const,
      render: (_: unknown, r: Task) => r.status === 'waiting' ? (
        <Space>
          <Popconfirm
            title="确认批准？"
            description={r.script ? `批准后将执行恢复脚本（${r.script.slice(0, 60)}${r.script.length > 60 ? '…' : ''}），请确认操作安全` : '批准后继续执行该任务'}
            onConfirm={() => doDecide(r.id, true)} okText="批准" cancelText="取消" okButtonProps={{ danger: true }}
          >
            <Button size="small" type="primary" icon={<CheckOutlined />}>批准</Button>
          </Popconfirm>
          <Button size="small" danger icon={<CloseOutlined />} onClick={() => doDecide(r.id, false)}>驳回</Button>
        </Space>
      ) : <Text type="secondary">—</Text> },
  ]

  const filtered = tasks.filter(t =>
    !search || t.id.includes(search) || (t.service || '').includes(search) || (t.context || '').includes(search))

  return (
    <Card
      title="审批中心"
      extra={<Space>
        <Input placeholder="搜索任务ID/服务/上下文" allowClear style={{ width: 240 }} onChange={e => setSearch(e.target.value)} />
        <Select allowClear placeholder="状态" style={{ width: 140 }}
          options={Object.entries(STATUS_MAP).map(([k, v]) => ({ value: k, label: v.label }))}
          onChange={v => setStatusFilter(v || '')} />
        <Button icon={<ReloadOutlined />} onClick={fetchTasks}>刷新</Button>
      </Space>}
    >
      <Table rowKey="id" columns={columns} dataSource={filtered} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />
    </Card>
  )
}

export default Approvals
