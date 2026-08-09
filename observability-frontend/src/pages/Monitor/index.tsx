import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Card, Empty, Form, Input, InputNumber, Modal, Select, Spin, Tag, Tooltip, message } from 'antd'
import { PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { Responsive, WidthProvider, type Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import 'react-resizable/css/styles.css'
import api, { DashboardPanel, listPanels, createPanel, updatePanel, deletePanel } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'

const darkText = '#e8e8e8'
const gridColor = 'rgba(255,255,255,0.12)'
const chartColors = ['#1677ff', '#52c41a', '#faad14', '#ff4d4f', '#13c2c2', '#722ed1', '#eb2f96']

const ResponsiveGridLayout = WidthProvider(Responsive)

// 从面板 grid 字段构造 lg 24 列布局。
// 位置有效性：grid_x>0 || grid_y>0 才用坐标（grid 全 0 表示未拖拽过的旧数据，用 sort/span 推导）。
// 注意：旧数据 grid_w 可能非 0（=span），但 grid_x/grid_y 为 0，故不能用 grid_w>0 判断"有位置"。
function buildLayout(panels: DashboardPanel[]): Layout[] {
  return panels.map((p) => {
    const w = p.grid_w > 0 ? p.grid_w : Math.min(Math.max(p.span || 6, 6), 24)
    const h = p.grid_h > 0 ? p.grid_h : 5
    const hasPos = p.grid_x > 0 || p.grid_y > 0
    const x = hasPos ? p.grid_x : p.sort % 24
    const y = hasPos ? p.grid_y : Math.floor(p.sort / 24)
    return { i: String(p.id), x, y, w, h, minW: 3, minH: 2 }
  })
}

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
  const [lgLayout, setLgLayout] = useState<Layout[]>([])
  const [saveLock, setSaveLock] = useState(false) // 拖拽/缩放中禁止并发保存
  // ref 保存最新 lg 布局，避免 onDragStop 触发时闭包读到 stale 的 allLayouts
  const lgLayoutRef = useRef<Layout[]>([])

  const loadPanels = async () => {
    try {
      const r = await listPanels()
      const ps = r?.data?.data || []
      setPanels(ps)
      setLgLayout(buildLayout(ps))
    } catch {
      setPanels([])
      setLgLayout([])
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
    setLgLayout(buildLayout(ps))
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
    form.setFieldsValue({ chart_type: 'line', span: 6, grid_h: 5 })
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
        const maxY = lgLayout.reduce((m, it) => Math.max(m, it.y + it.h), 0)
        await createPanel({
          ...values,
          enabled: true,
          grid_x: 0,
          grid_y: maxY,
          grid_w: values.span || 6,
          grid_h: values.grid_h || 5,
        })
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

  // 拖拽/缩放结束后持久化布局（用 lgLayoutRef 的 24 列坐标，避免 stale closure），仅写有变化的面板
  const persistLayout = async () => {
    const layouts = lgLayoutRef.current
    if (!layouts || layouts.length === 0 || saveLock) return
    setSaveLock(true)
    try {
      await Promise.all(
        layouts.map(async (it) => {
          const panel = panels.find((p) => String(p.id) === it.i)
          if (!panel) return
          const changed =
            it.x !== panel.grid_x || it.y !== panel.grid_y ||
            it.w !== panel.grid_w || it.h !== panel.grid_h
          if (!changed) return
          await updatePanel(panel.id, { grid_x: it.x, grid_y: it.y, grid_w: it.w, grid_h: it.h })
        }),
      )
    } catch {
      // 保存失败不阻塞交互，下次拖拽会重试
    } finally {
      setSaveLock(false)
    }
  }

  // 拖动/缩放过程中同步布局（lg 为持久化基准）；同时更新 ref 供持久化读取
  const handleLayoutChange = (current: Layout[], all: { [key: string]: Layout[] }) => {
    const lg = all?.lg || current
    if (lg && lg.length) {
      setLgLayout(lg)
      lgLayoutRef.current = lg
    }
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
          <ResponsiveGridLayout
            layouts={{ lg: lgLayout, md: lgLayout, sm: lgLayout, xs: lgLayout }}
            breakpoints={{ lg: 1200, md: 992, sm: 768, xs: 480 }}
            cols={{ lg: 24, md: 16, sm: 12, xs: 6 }}
            rowHeight={60}
            margin={[12, 12]}
            draggableHandle=".panel-drag-handle"
            onLayoutChange={handleLayoutChange}
            onDragStop={persistLayout}
            onResizeStop={persistLayout}
          >
            {sortedPanels.map((p) => {
              const series = dataMap[p.id] || []
              return (
                <div key={String(p.id)} style={{ overflow: 'hidden' }}>
                  <Card
                    title={
                      <span className="panel-drag-handle" style={{ cursor: 'move', fontSize: 13 }}>
                        {p.title}
                        <Tag style={{ marginLeft: 8 }}>{p.chart_type}</Tag>
                      </span>
                    }
                    extra={
                      <div style={{ display: 'flex', gap: 4 }}>
                        <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(p)} />
                        <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(p)} />
                      </div>
                    }
                    style={{ borderRadius: 12, height: '100%' }}
                    bodyStyle={{ height: 'calc(100% - 57px)' }}
                  >
                    {series.length ? (
                      <ReactECharts option={buildOption(p, series)} style={{ height: '100%' }} notMerge />
                    ) : (
                      <Empty description="暂无数据" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }} />
                    )}
                  </Card>
                </div>
              )
            })}
          </ResponsiveGridLayout>
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
          <Form.Item name="grid_h" label="高度（行数，2-12）">
            <InputNumber min={2} max={12} defaultValue={5} style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Monitor
