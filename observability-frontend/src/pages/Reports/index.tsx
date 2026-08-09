import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Input, Select, Button, Empty, Row, Col, message } from 'antd'
import { ReloadOutlined, DownloadOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import api from '../../api/client'
import { listReports, reportTrend } from '../../api/client'

const VERDICT_MAP: Record<string, { color: string; label: string }> = {
  ok: { color: 'green', label: '正常' },
  warning: { color: 'orange', label: '警告' },
  critical: { color: 'red', label: '严重' },
}

const tooltipStyle = { background: '#1a1a1a', border: '1px solid #333', textStyle: { color: '#e8e8e8' } }

interface ReportItem {
  task_id: string; service_name: string; report_type: string; verdict: string
  risk_score: number; summary: string; created_at: string
}

const Reports: React.FC = () => {
  const [items, setItems] = useState<ReportItem[]>([])
  const [count, setCount] = useState(0)
  const [loading, setLoading] = useState(false)
  const [service, setService] = useState('')
  const [search, setSearch] = useState('')

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string> = {}
      if (service) params.service = service
      const r = await listReports({ ...params, limit: 100, offset: 0 })
      setItems(r.data?.reports || [])
      setCount(r.data?.count || 0)
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [service])

  useEffect(() => { fetch() }, [fetch])

  const handleDownload = async (item: ReportItem) => {
    try {
      // 对齐后端实际路由 /ops/reports/{task_id}/download（经 query-api 代理 + JWT）
      const r = await api.get(`/ops/reports/${item.task_id}/download`, { responseType: 'blob' })
      const url = URL.createObjectURL(r.data)
      const a = document.createElement('a')
      a.href = url
      a.download = `${item.service_name || 'report'}-${item.task_id}.md`
      a.click()
      URL.revokeObjectURL(url)
    } catch { message.warning('该报告无下载文件（仅元数据）') }
  }

  // 风险分布（环形）
  const riskDist = items.reduce((acc, r) => {
    const v = r.verdict || 'ok'
    acc[v] = (acc[v] || 0) + 1
    return acc
  }, {} as Record<string, number>)
  const pieOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'item', ...tooltipStyle },
    legend: { bottom: 0, textStyle: { color: '#e8e8e8' } },
    series: [{
      type: 'pie', radius: ['45%', '70%'], center: ['50%', '44%'],
      label: { show: false },
      data: [
        { value: riskDist.ok || 0, name: '正常', itemStyle: { color: '#52c41a' } },
        { value: riskDist.warning || 0, name: '警告', itemStyle: { color: '#faad14' } },
        { value: riskDist.critical || 0, name: '严重', itemStyle: { color: '#ff4d4f' } },
      ],
    }],
  }

  // 风险分趋势（按最新若干条）
  const trendData = items.slice(0, 30).reverse()
  const trendOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', ...tooltipStyle },
    grid: { left: 40, right: 20, top: 20, bottom: 30 },
    xAxis: { type: 'category', data: trendData.map((_, i) => i + 1), axisLabel: { color: '#999' } },
    yAxis: { type: 'value', max: 100, axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: '#1f1f1f' } } },
    series: [{ name: '风险分', type: 'line', smooth: true, data: trendData.map(r => r.risk_score), itemStyle: { color: '#1677ff' }, areaStyle: { opacity: 0.15 } }],
  }

  const filtered = items.filter(it =>
    !search || it.service_name?.includes(search) || it.task_id?.includes(search) || it.summary?.includes(search))

  const columns = [
    { title: '任务ID', dataIndex: 'task_id', key: 'task_id', width: 170, ellipsis: true },
    { title: '服务', dataIndex: 'service_name', key: 'service_name', width: 150,
      render: (v: string) => v && v !== '-' ? <Tag color="blue">{v}</Tag> : '-' },
    { title: '类型', dataIndex: 'report_type', key: 'report_type', width: 100 },
    { title: '结论', dataIndex: 'verdict', key: 'verdict', width: 90,
      render: (v: string) => VERDICT_MAP[v] ? <Tag color={VERDICT_MAP[v].color}>{VERDICT_MAP[v].label}</Tag> : (v || '-') },
    { title: '风险分', dataIndex: 'risk_score', key: 'risk_score', width: 90,
      render: (v: number) => {
        const n = Number(v || 0)
        const color = n >= 70 ? 'red' : n >= 40 ? 'orange' : 'green'
        return <Tag color={color}>{n}</Tag>
      } },
    { title: '摘要', dataIndex: 'summary', key: 'summary', ellipsis: true },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', width: 170 },
    { title: '操作', key: 'action', width: 80, fixed: 'right' as const,
      render: (_: unknown, r: ReportItem) => (
        <Button size="small" icon={<DownloadOutlined />} onClick={() => handleDownload(r)}>下载</Button>
      ) },
  ]

  return (
    <div>
      <Card
        title={`报告中心（${count}）`}
        extra={<Space>
          <Input placeholder="搜索服务/任务/摘要" allowClear style={{ width: 220 }} onChange={e => setSearch(e.target.value)} />
          <Select allowClear placeholder="服务" style={{ width: 160 }}
            options={[...new Set(items.map(i => i.service_name).filter(Boolean))].map(s => ({ value: s, label: s }))}
            onChange={v => setService(v || '')} />
          <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
        </Space>}
        style={{ marginBottom: 16 }}
      >
        <Row gutter={[16, 16]}>
          <Col xs={24} md={8}>
            <Card size="small" title="巡检结论分布">
              {count > 0 ? <ReactECharts option={pieOption} style={{ height: 200 }} /> : <Empty />}
            </Card>
          </Col>
          <Col xs={24} md={16}>
            <Card size="small" title="风险分趋势（最近报告）">
              {trendData.length > 1 ? <ReactECharts option={trendOption} style={{ height: 200 }} /> : <Empty />}
            </Card>
          </Col>
        </Row>
      </Card>

      <Card>
        <Table rowKey="task_id" columns={columns} dataSource={filtered} loading={loading}
          pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }}
          scroll={{ x: 'max-content' }} />
      </Card>
    </div>
  )
}

export default Reports
