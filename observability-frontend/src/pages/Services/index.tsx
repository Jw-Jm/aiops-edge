import React, { useEffect, useState } from 'react'
import { Table, Input, Card, Statistic, Row, Col, Spin, Drawer, Button } from 'antd'
import { SearchOutlined, ArrowRightOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { getServices } from '../../api/client'
import ServiceDetail from '../ServiceDetail'

const Services: React.FC = () => {
  const [services, setServices] = useState<Array<Record<string, unknown>>>([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    getServices().then(r => {
      setServices(Array.isArray(r.data) ? r.data : (r.data?.data || []))
    }).finally(() => setLoading(false))
  }, [])

  const filtered = services.filter((s: Record<string, unknown>) =>
    !search || String(s.service_name || '').toLowerCase().includes(search.toLowerCase()))

  const totalServices = services.length
  const totalTraces = services.reduce((sum: number, s: Record<string, unknown>) => sum + (Number(s.traces) || 0), 0)

  const columns = [
    { title: '服务名称', dataIndex: 'service_name', key: 'name', width: 220, render: (n: string) => <a onClick={() => setSelected(n)}>{n}</a> },
    { title: '调用链', dataIndex: 'traces', key: 'traces', width: 110, align: 'right' as const, sorter: (a: Record<string, unknown>, b: Record<string, unknown>) => Number(a.traces) - Number(b.traces) },
    { title: 'Span 数', dataIndex: 'spans', key: 'spans', width: 110, align: 'right' as const, sorter: (a: Record<string, unknown>, b: Record<string, unknown>) => Number(a.spans) - Number(b.spans) },
    { title: '平均延迟 (ms)', dataIndex: 'avg_ms', key: 'avg_ms', width: 130, align: 'right' as const, render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{(v || 0).toFixed(1)}</span> },
    { title: '最大延迟 (ms)', dataIndex: 'max_ms', key: 'max_ms', width: 130, align: 'right' as const, render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{(v || 0).toFixed(1)}</span> },
  ]

  return (
    <div>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={8}><Card><Statistic title="服务总数" value={totalServices} /></Card></Col>
        <Col span={8}><Card><Statistic title="Trace 总数" value={totalTraces} /></Card></Col>
        <Col span={8}>
          <Card><Statistic title="数据状态"
            value={loading ? '加载中' : (totalServices > 0 ? '正常' : '暂无数据')}
            valueStyle={{ color: totalServices > 0 ? '#52c41a' : '#faad14' }} />
          </Card>
        </Col>
      </Row>
      <Card>
        <Input prefix={<SearchOutlined />} placeholder="搜索服务名..." value={search} onChange={e => setSearch(e.target.value)} style={{ width: 300, marginBottom: 16 }} />
        <Spin spinning={loading}>
          <Table dataSource={filtered} columns={columns} rowKey="service_name" size="middle" pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 个服务` }} />
        </Spin>
      </Card>

      <Drawer
        title={selected ? `服务详情 · ${selected}` : '服务详情'}
        placement="right"
        width={760}
        open={!!selected}
        onClose={() => setSelected(null)}
        extra={selected && <Button type="primary" size="small" icon={<ArrowRightOutlined />} onClick={() => { const n = selected; setSelected(null); navigate(`/services/${encodeURIComponent(n)}`) }}>完整页面</Button>}
      >
        {selected && <ServiceDetail key={selected} name={selected} />}
      </Drawer>
    </div>
  )
}

export default Services
