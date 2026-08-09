import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Input, Select, Button, Empty, Row, Col, message, Tooltip } from 'antd'
import { ReloadOutlined, DownloadOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import api from '../../api/client'
import { listReports, reportTrend } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

// 兼容后端返回的中文 verdict 与英文 verdict：统一归一化
const VERDICT_MAP: Record<string, { color: string; label: string }> = {
  // 英文
  ok: { color: 'green', label: '正常' },
  warning: { color: 'orange', label: '警告' },
  critical: { color: 'red', label: '严重' },
  // 中文（后端 _extract_report_fields 返回）
  '健康': { color: 'green', label: '正常' },
  '正常': { color: 'green', label: '正常' },
  '关注': { color: 'orange', label: '警告' },
  '警告': { color: 'orange', label: '警告' },
  '异常': { color: 'red', label: '严重' },
  '高危': { color: 'red', label: '严重' },
  '严重': { color: 'red', label: '严重' },
}

const NORMALIZE_VERDICT: Record<string, string> = {
  '健康': 'ok', '正常': 'ok', ok: 'ok', healthy: 'ok',
  '关注': 'warning', '警告': 'warning', warning: 'warning',
  '异常': 'critical', '高危': 'critical', '严重': 'critical', critical: 'critical',
}

// 风险分 0~1 浮点 → 百分比
const riskPct = (v: number) => Math.round(Number(v || 0) * 100)

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

  // 风险分布（环形）—— 先归一化 verdict 再统计，兼容中文/英文
  const riskDist = items.reduce((acc, r) => {
    const v = NORMALIZE_VERDICT[r.verdict] || 'ok'
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
    series: [{ name: '风险分', type: 'line', smooth: true, data: trendData.map(r => riskPct(Number(r.risk_score || 0))), itemStyle: { color: '#1677ff' }, areaStyle: { opacity: 0.15 } }],
  }

  const filtered = items.filter(it =>
    !search || it.service_name?.includes(search) || it.task_id?.includes(search) || it.summary?.includes(search))

  const columns = [
    { title: '任务ID', dataIndex: 'task_id', key: 'task_id', width: 170, ellipsis: true },
    { title: '服务', dataIndex: 'service_name', key: 'service_name', width: 150,
      render: (v: string) => {
        const svc = !v || v === 'unknown' || v === '-' ? null : v
        return svc ? <Tag color="blue">{svc}</Tag> : '-'
      } },
    { title: '类型', dataIndex: 'report_type', key: 'report_type', width: 100,
      render: (v: string) => {
        const map: Record<string, string> = { inspection: '巡检', diagnosis: '诊断', recovery: '恢复', analysis: '分析' }
        return map[v] || v || '-'
      } },
    { title: '结论', dataIndex: 'verdict', key: 'verdict', width: 90,
      render: (v: string) => {
        if (!v || v === 'unknown') return '-'
        const norm = NORMALIZE_VERDICT[v]
        const mapped = VERDICT_MAP[norm || v] || VERDICT_MAP[v]
        return mapped ? <Tag color={mapped.color}>{mapped.label}</Tag> : v
      } },
    { title: '风险分', dataIndex: 'risk_score', key: 'risk_score', width: 90,
      render: (v: number) => {
        // risk_score 为 0~1 浮点，×100 转百分比后按高/中/低分档
        const pct = riskPct(Number(v || 0))
        const color = pct >= 70 ? 'red' : pct >= 40 ? 'orange' : 'green'
        return <Tag color={color}>{pct}%</Tag>
      } },
    { title: '摘要', dataIndex: 'summary', key: 'summary', ellipsis: true,
      render: (v: string) => v ? <Tooltip title={v}><span>{v}</span></Tooltip> : '-' },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', width: 170, render: (v: string) => fmtLocalTime(v) },
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
