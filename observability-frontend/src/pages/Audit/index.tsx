import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Input, Select, Button, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import api from '../../api/client'

const { Text } = Typography

interface AuditLog {
  id: number; task_id: string; action: string; operator: string
  target_service: string; command: string; result: string; detail: string
  created_at: string
}

const RESULT_MAP: Record<string, { color: string; label: string }> = {
  ok:     { color: 'green', label: '成功' },
  fail:   { color: 'red', label: '失败' },
  error:  { color: 'red', label: '错误' },
  denied: { color: 'orange', label: '拒绝' },
}

const ACTION_COLORS: Record<string, string> = {
  execute: 'blue', approve: 'green', reject: 'red', diagnose: 'cyan',
  skill: 'purple', flow: 'geekblue', rca: 'magenta', run: 'blue', login: 'default',
}

const Audit: React.FC = () => {
  const [logs, setLogs] = useState<AuditLog[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [action, setAction] = useState('')
  const [operator, setOperator] = useState('')
  const [service, setService] = useState('')

  const fetchLogs = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string | number> = { page, size: 50 }
      if (action) params.action = action
      if (operator) params.operator = operator
      if (service) params.service = service
      const r = await api.get('/ops/audit-logs', { params })
      setLogs(r.data?.items || [])
      setTotal(r.data?.total || 0)
    } catch { /* ignore */ }
    setLoading(false)
  }, [page, action, operator, service])

  useEffect(() => { fetchLogs() }, [fetchLogs])

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 80 },
    { title: '动作', dataIndex: 'action', key: 'action', width: 110,
      render: (v: string) => <Tag color={ACTION_COLORS[v] || 'default'}>{v}</Tag> },
    { title: '操作者', dataIndex: 'operator', key: 'operator', width: 120 },
    { title: '目标服务', dataIndex: 'target_service', key: 'target_service', width: 160,
      render: (v: string) => v ? <Tag color="blue">{v}</Tag> : '-' },
    { title: '命令', dataIndex: 'command', key: 'command', ellipsis: true },
    { title: '结果', dataIndex: 'result', key: 'result', width: 100,
      render: (v: string) => RESULT_MAP[v] ? <Tag color={RESULT_MAP[v].color}>{RESULT_MAP[v].label}</Tag> : (v || '-') },
    { title: '任务ID', dataIndex: 'task_id', key: 'task_id', width: 160, ellipsis: true },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', width: 180 },
  ]

  return (
    <Card
      title="审计日志"
      extra={<Space>
        <Select allowClear placeholder="动作" style={{ width: 130 }}
          options={['execute', 'approve', 'reject', 'diagnose', 'skill', 'flow', 'rca', 'run'].map(a => ({ value: a, label: a }))}
          onChange={v => { setAction(v || ''); setPage(1) }} />
        <Input placeholder="操作者" allowClear style={{ width: 130 }} onChange={e => { setOperator(e.target.value); setPage(1) }} />
        <Input placeholder="目标服务" allowClear style={{ width: 140 }} onChange={e => { setService(e.target.value); setPage(1) }} />
        <Button icon={<ReloadOutlined />} onClick={fetchLogs}>刷新</Button>
      </Space>}
    >
      <Table rowKey="id" columns={columns} dataSource={logs} loading={loading}
        pagination={{ current: page, pageSize: 50, total, onChange: setPage, showTotal: (t: number) => `共 ${t} 条` }}
        expandable={{ expandedRowRender: (r: AuditLog) => r.detail ? (
          <pre style={{ margin: 0, whiteSpace: 'pre-wrap', fontSize: 12 }}>{r.detail}</pre>
        ) : <Text type="secondary">无详情</Text> }}
      />
    </Card>
  )
}

export default Audit
