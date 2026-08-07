import React, { useEffect, useState } from 'react'
import { Table, Input, Card, Spin, Tag } from 'antd'
import { SearchOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { getTraces } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const Traces: React.FC = () => {
  const [traces, setTraces] = useState<Array<Record<string, unknown>>>([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const navigate = useNavigate()

  useEffect(() => {
    getTraces({ limit: 50 }).then(r => {
      setTraces(Array.isArray(r.data) ? r.data : (r.data?.data || []))
    }).finally(() => setLoading(false))
  }, [])

  const columns = [
    {
      title: 'Trace ID', dataIndex: 'trace_id', key: 'trace_id', width: 220,
      render: (id: string) => <a onClick={() => navigate(`/traces/${id}`)} style={{ fontFamily: 'monospace' }}>{(id || '').slice(0, 16)}...</a>
    },
    { title: '开始时间', dataIndex: 'start', key: 'start', width: 170, render: (t: string) => fmtLocalTime(t, '-', 'MM-DD HH:mm:ss') },
    { title: '服务数', dataIndex: 'services', key: 'services', width: 90, align: 'center' as const, render: (v: number) => <Tag color="blue">{v}</Tag> },
    { title: 'Span 数', dataIndex: 'spans', key: 'spans', width: 90, align: 'right' as const },
    { title: '最大延迟 (ms)', dataIndex: 'max_ms', key: 'max_ms', width: 120, align: 'right' as const, render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{(v || 0).toFixed(1)}</span> },
  ]

  const filtered = traces.filter((t: Record<string, unknown>) =>
    !search || String(t.trace_id || '').toLowerCase().includes(search.toLowerCase()))

  return (
    <Card>
      <Input prefix={<SearchOutlined />} placeholder="搜索 Trace ID..." value={search} onChange={e => setSearch(e.target.value)} style={{ width: 400, marginBottom: 16 }} />
      <Spin spinning={loading}>
        <Table dataSource={filtered} columns={columns} rowKey="trace_id" size="middle" pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 条调用链` }} />
      </Spin>
    </Card>
  )
}

export default Traces
