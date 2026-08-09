import React, { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, Table, Tag, Select, Button, Space, Typography, message } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { Artifact, listArtifacts } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'

const { Text } = Typography

const TYPE_COLORS: Record<string, string> = {
  report: 'blue',
  approval: 'orange',
  flow_run: 'green',
}
const TYPE_LABELS: Record<string, string> = {
  report: '报告',
  approval: '审批',
  flow_run: '工作流',
}

const Artifacts: React.FC = () => {
  const navigate = useNavigate()
  const [items, setItems] = useState<Artifact[]>([])
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [loading, setLoading] = useState(false)

  const load = async (tf: string) => {
    setLoading(true)
    try {
      const r = await listArtifacts({ limit: 100, type_filter: tf || undefined })
      setItems(r?.data?.artifacts || [])
    } catch {
      setItems([])
      message.error('加载产物失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load(typeFilter) }, [typeFilter])

  const statusColor = (s: string) => {
    if (/done|approved|healthy|pass/i.test(s)) return 'green'
    if (/waiting|queued|diagnosing|running|关注/i.test(s)) return 'orange'
    if (/failed|rejected|异常/i.test(s)) return 'red'
    return 'default'
  }

  const columns = [
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      width: 90,
      render: (t: string) => <Tag color={TYPE_COLORS[t] || 'default'}>{TYPE_LABELS[t] || t}</Tag>,
    },
    { title: '标题', dataIndex: 'title', key: 'title', ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 110,
      render: (s: string) => <Tag color={statusColor(s)}>{s || '-'}</Tag>,
    },
    { title: '服务', dataIndex: 'service', key: 'service', width: 140, render: (s: string) => s || '-' },
    { title: '时间', dataIndex: 'time', key: 'time', width: 180, render: (t: string) => (t ? String(t).slice(0, 19) : '-') },
    { title: '摘要', dataIndex: 'summary', key: 'summary', ellipsis: true },
    {
      title: '操作',
      key: 'action',
      width: 90,
      render: (_: any, r: Artifact) => (
        <Button size="small" type="link" onClick={() => navigate(r.detail_url)}>查看</Button>
      ),
    },
  ]

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Space>
          <span style={{ fontSize: 16, fontWeight: 600 }}>产物中心</span>
          <Text type="secondary" style={{ fontSize: 12 }}>统一聚合报告 / 审批单 / 工作流运行</Text>
        </Space>
        <Space>
          <Select
            allowClear
            placeholder="按类型筛选"
            style={{ width: 140 }}
            value={typeFilter || undefined}
            onChange={(v) => setTypeFilter(v || '')}
            options={Object.entries(TYPE_LABELS).map(([k, v]) => ({ value: k, label: v }))}
          />
          <Button icon={<ReloadOutlined />} onClick={() => load(typeFilter)}>刷新</Button>
        </Space>
      </div>

      <Card style={{ borderRadius: 12 }}>
        {items.length === 0 ? (
          <AppEmpty description="暂无产物" tip="报告/审批单/工作流运行会出现在这里" height={200} />
        ) : (
          <Table
            rowKey={(r) => `${r.type}:${r.id}`}
            columns={columns}
            dataSource={items}
            loading={loading}
            pagination={{ pageSize: 20, showSizeChanger: false }}
            size="middle"
          />
        )}
      </Card>
    </div>
  )
}

export default Artifacts
