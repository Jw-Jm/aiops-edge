import { useEffect, useMemo, useState } from 'react'
import { Button, Card, Col, Empty, Form, Input, InputNumber, Modal, Row, Select, Spin, Tag, Tooltip, message } from 'antd'
import { PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined, ArrowUpOutlined, ArrowDownOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import api, { DashboardPanel, listPanels, createPanel, updatePanel, deletePanel } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'

const darkText = '#e8e8e8'
const gridColor = 'rgba(255,255,255,0.12)'
const chartColors = ['#1677ff', '#52c41a', '#faad14', '#ff4d4f', '#13c2c2', '#722ed1', '#eb2f96']

// query_range 结果 → echarts series 数据
function buildOption(panel: DashboardPanel, series: any[]) {
  const xValues = series[0]?.values?.map((v: any[]) => new Date(v[0] * 1000).toLocaleTimeString()) || []
  const type = panel.chart_type === 'bar' ? 'bar' : panel.chart_type === 'gauge' ? 'line' : panel.chart_type === 'table' ? 'line' : 'line'
  const isArea = panel.chart_type === 'area'
  return {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', textStyle: { fontSize: 12 } },
    legend: { data: series.map((s, i) => s.metric?.service || `series${i}`), textStyle: { color: darkText }, top: 0 },
    grid: { left: 50, right: 20, top: 30, bottom: 30 },
    xAxis: { type: 'category', data: xValues, axisLabel: { color: '#999' }, axisLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    series: series.map((s, i) => ({
      name: s.metric?.service || `series${i}`,
      type,
      smooth: true,
      data: s.values?.map((v: any[]) => Number(v[1])),
      itemStyle: { color: chartColors[i % chartColors.length] },
      areaStyle: isArea ? { opacity: 0.15 } : undefined,
    })),
  }
}

const Monitor: React.FC = () => {
  const [panels, setPanels] = useState<DashboardPanel[]>([])
  const [dataMap, setDataMap] = useState<Record<string, any[]>>({})
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<DashboardPanel | null>(null)
  const [form] = Form.useForm()

  const loadPanels = async () => {
    try {
      const r = await listPanels()
      setPanels(r?.data?.data || [])
    } catch {
      setPanels([])
    }
  }

  const loadData = async (ps: DashboardPanel[]) => {
    const now = Math.floor(Date.now() / 1000)
    const start = now - 3600
    const results: Record<string, any[]> = {}
    await Promise.all(
      ps.filter((p) => p.enabled && p.query).map(async (p) => {
        try {
          const r = await api.get('/metrics/query_range', { params: { query: p.query, start, end: now, step: '60' } })
          results[p.id] = r?.data?.data?.result || []
        } catch {
          results[p.id] = []
        }
      }),
    )
    setDataMap(results)
  }

  const load = async () => {
    setLoading(true)
    await loadPanels()
    setLoading(false)
  }

  const refresh = async () => {
    setLoading(true)
    const ps = await listPanels().then((r) => r?.data?.data || [])
    setPanels(ps)
    await loadData(ps)
    setLoading(false)
  }

  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    if (panels.length) loadData(panels)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [panels.length])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ chart_type: 'line', span: 6 })
    setModalOpen(true)
  }

  const openEdit = (p: DashboardPanel) => {
    setEditing(p)
    form.setFieldsValue(p)
    setModalOpen(true)
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields()
      if (editing) {
        await updatePanel(editing.id, { ...values, enabled: editing.enabled })
      } else {
        await createPanel({ ...values, enabled: true })
      }
      message.success('面板已保存')
      setModalOpen(false)
      await refresh()
    } catch (err: any) {
      if (err?.errorFields) return
      message.error('保存失败')
    }
  }

  const handleDelete = async (p: DashboardPanel) => {
    await deletePanel(p.id)
    message.success('面板已删除')
    await refresh()
  }

  // 上移/下移排序（交换 sort）
  const move = async (index: number, dir: -1 | 1) => {
    const target = index + dir
    if (target < 0 || target >= panels.length) return
    const sorted = [...panels].sort((a, b) => a.sort - b.sort)
    const tmp = sorted[index]
    sorted[index] = sorted[target]
    sorted[target] = tmp
    for (let i = 0; i < sorted.length; i++) {
      await updatePanel(sorted[i].id, { ...sorted[i], sort: i })
    }
    await refresh()
  }

  const sortedPanels = useMemo(() => [...panels].sort((a, b) => a.sort - b.sort), [panels])

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div style={{ fontSize: 16, fontWeight: 600 }}>监控看板</div>
        <div>
          <Tooltip title="刷新"><Button icon={<ReloadOutlined />} onClick={refresh} style={{ marginRight: 8 }} /></Tooltip>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新增面板</Button>
        </div>
      </div>

      <Spin spinning={loading}>
        {sortedPanels.length === 0 ? (
          <AppEmpty description="暂无面板" tip="点击右上角新增面板开始" height={200} />
        ) : (
          <Row gutter={[16, 16]}>
            {sortedPanels.map((p, idx) => {
              const span = Math.min(Math.max(p.span || 6, 6), 24)
              const series = dataMap[p.id] || []
              return (
                <Col span={span} key={p.id}>
                  <Card
                    title={
                      <span style={{ fontSize: 13 }}>
                        {p.title}
                        <Tag style={{ marginLeft: 8 }}>{p.chart_type}</Tag>
                      </span>
                    }
                    extra={
                      <div style={{ display: 'flex', gap: 4 }}>
                        <Button size="small" icon={<ArrowUpOutlined />} disabled={idx === 0} onClick={() => move(idx, -1)} />
                        <Button size="small" icon={<ArrowDownOutlined />} disabled={idx === sortedPanels.length - 1} onClick={() => move(idx, 1)} />
                        <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(p)} />
                        <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(p)} />
                      </div>
                    }
                    style={{ borderRadius: 12 }}
                  >
                    {series.length ? (
                      <ReactECharts option={buildOption(p, series)} style={{ height: 260 }} notMerge />
                    ) : (
                      <Empty description="暂无数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                    )}
                  </Card>
                </Col>
              )
            })}
          </Row>
        )}
      </Spin>

      <Modal
        title={editing ? '编辑面板' : '新增面板'}
        open={modalOpen}
        onOk={handleSave}
        onCancel={() => setModalOpen(false)}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item name="title" label="标题" rules={[{ required: true }]}>
            <Input placeholder="面板标题" />
          </Form.Item>
          <Form.Item name="query" label="PromQL 查询" rules={[{ required: true }]} extra="如 sum(rate(http_requests_total[5m])) by (service)">
            <Input.TextArea placeholder="PromQL 表达式" rows={3} />
          </Form.Item>
          <Form.Item name="chart_type" label="图表类型">
            <Select>
              <Select.Option value="line">折线图</Select.Option>
              <Select.Option value="bar">柱状图</Select.Option>
              <Select.Option value="area">面积图</Select.Option>
              <Select.Option value="gauge">仪表盘</Select.Option>
              <Select.Option value="table">表格</Select.Option>
            </Select>
          </Form.Item>
          <Form.Item name="span" label="宽度（栅格数，6-24）">
            <InputNumber min={6} max={24} step={2} style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Monitor
