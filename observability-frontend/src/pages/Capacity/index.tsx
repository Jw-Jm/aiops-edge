import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Empty, Row, Select, Space, Spin, Statistic, Tag, Tooltip } from 'antd'
import { ReloadOutlined, ArrowUpOutlined, ArrowDownOutlined, ArrowRightOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { CapacityForecast, ForecastSeries, getCapacityForecast, getCapacityInstances } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'

const darkText = '#e8e8e8'
const gridColor = 'rgba(255,255,255,0.12)'

const METRICS = [
  { key: 'cpu', label: 'CPU 使用率', unit: '%' },
  { key: 'memory', label: '内存使用率', unit: '%' },
  { key: 'disk', label: '磁盘使用率', unit: '%' },
  { key: 'network', label: '网络带宽', unit: 'bps' },
]

// 把 ETT 秒数格式化为可读字符串
function formatETT(sec: number): string {
  if (sec <= 0) return '—'
  if (sec < 60) return `${Math.round(sec)} 秒`
  if (sec < 3600) return `${(sec / 60).toFixed(0)} 分钟`
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} 小时`
  return `${(sec / 86400).toFixed(1)} 天`
}

// 单维度预测卡片
function ForecastCard({ title, fc, isEwma }: { title: string; fc: ForecastSeries; isEwma: boolean }) {
  if (!fc || !fc.values?.length) return <Card title={title} size="small" style={{ borderRadius: 12 }}><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无预测" /></Card>
  const last = fc.values[fc.values.length - 1]
  const trend = fc.values.length >= 2 && fc.values[fc.values.length - 1] > fc.values[0]
  const status = fc.already_breached
    ? <Tag color="red">已超阈值</Tag>
    : fc.within_horizon
      ? <Tag color="orange">预测期内达到</Tag>
      : <Tag color="green">预测期内安全</Tag>
  return (
    <Card title={title} size="small" style={{ borderRadius: 12 }}>
      <Space direction="vertical" size={8}>
        <div>当前预测末值：{last.toFixed(1)}</div>
        <div>{trend ? <ArrowUpOutlined style={{ color: '#ff4d4f' }} /> : <ArrowRightOutlined />} 趋势方向：{trend ? '上升' : '平稳/下降'}</div>
        <div>
          预计达阈值时间：{fc.within_horizon ? formatETT(fc.ett_seconds) : '预测期内不会达到'} {status}
        </div>
        {isEwma ? <Tag color="blue">平滑预测，可能偏乐观</Tag> : null}
      </Space>
    </Card>
  )
}

// 组装 echarts option：历史 + 线性预测 + EWMA 预测 + 阈值 markLine
function buildOption(data: CapacityForecast, threshold: number) {
  const histTime = data.timestamps.slice(0, data.history.length).map((t) => new Date(t * 1000).toLocaleTimeString())
  // 预测 x 轴：历史最后一个时间点 + 预测各点
  const predTime = data.timestamps.slice(data.history.length - 1).map((t) => new Date(t * 1000).toLocaleTimeString())
  // 历史曲线：前 n 个点；预测曲线：拼一个衔接点(history末值)保证连续
  const histData = data.history.map((v, i) => [histTime[i], v])
  const lastHist = data.history.length ? data.history[data.history.length - 1] : 0
  const linearData = predTime.map((t, i) => {
    const v = i === 0 ? lastHist : data.forecasts.linear.values[i - 1]
    return [t, v]
  })
  const ewmaData = predTime.map((t, i) => {
    const v = i === 0 ? lastHist : data.forecasts.ewma.values[i - 1]
    return [t, v]
  })
  return {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', textStyle: { fontSize: 12 } },
    legend: { data: ['历史', '线性回归预测', 'EWMA 预测'], textStyle: { color: darkText }, top: 0 },
    grid: { left: 60, right: 20, top: 30, bottom: 30 },
    xAxis: { type: 'category', data: histTime.concat(predTime.slice(1)), axisLabel: { color: '#999' }, axisLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    series: [
      {
        name: '历史', type: 'line', smooth: true, data: histData, itemStyle: { color: '#1677ff' }, symbol: 'none',
        // markLine 必须挂在 series 内部；仅挂在历史 series 上避免重复画阈值线
        markLine: data.history.length ? {
          symbol: 'none',
          label: { formatter: `阈值 ${threshold}`, color: '#ff4d4f' },
          lineStyle: { color: '#ff4d4f', type: 'dashed' },
          data: [{ yAxis: threshold }],
        } : undefined,
      },
      { name: '线性回归预测', type: 'line', smooth: true, data: linearData, itemStyle: { color: '#52c41a' }, symbol: 'none', lineStyle: { type: 'solid' } },
      { name: 'EWMA 预测', type: 'line', smooth: true, data: ewmaData, itemStyle: { color: '#faad14' }, symbol: 'none', lineStyle: { type: 'dashed' } },
    ],
  }
}

const Capacity: React.FC = () => {
  const [metric, setMetric] = useState('cpu')
  const [hours, setHours] = useState(24)
  const [horizon, setHorizon] = useState(12)
  const [instance, setInstance] = useState('') // '' = 全部节点（集群聚合）
  const [instances, setInstances] = useState<string[]>([])
  const [data, setData] = useState<CapacityForecast | null>(null)
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const r = await getCapacityForecast({ metric, hours, horizon, instance: instance || undefined })
      setData(r?.data || null)
    } catch {
      setData(null)
    } finally {
      setLoading(false)
    }
  }

  // 加载可选 node 列表（VM 中 node-exporter 实例）
  useEffect(() => {
    getCapacityInstances()
      .then((r) => setInstances(r?.data?.instances || []))
      .catch(() => setInstances([]))
  }, [])

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [metric, hours, horizon, instance])

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div style={{ fontSize: 16, fontWeight: 600 }}>容量预测</div>
        <Space wrap>
          <Select
            value={instance}
            onChange={setInstance}
            style={{ width: 220 }}
            placeholder="选择节点"
            options={[
              { value: '', label: '全部节点（集群聚合）' },
              ...instances.map((i) => ({ value: i, label: i })),
            ]}
          />
          <Select value={hours} onChange={setHours} style={{ width: 120 }} options={[12, 24, 48, 72].map((h) => ({ value: h, label: `历史 ${h}h` }))} />
          <Select value={horizon} onChange={setHorizon} style={{ width: 140 }} options={[6, 12, 24, 48].map((h) => ({ value: h, label: `预测 ${h} 步` }))} />
          <Tooltip title="刷新"><Button icon={<ReloadOutlined />} onClick={load} /></Tooltip>
        </Space>
      </div>

      {/* 维度切换 */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {METRICS.map((m) => (
          <Col span={6} key={m.key}>
            <Card
              hoverable
              onClick={() => setMetric(m.key)}
              style={{ borderRadius: 12, cursor: 'pointer', borderColor: metric === m.key ? '#1677ff' : undefined }}
            >
              <Statistic title={m.label} value={metric === m.key && data ? data.current.toFixed(1) : '—'} suffix={m.unit} />
              {metric === m.key && data ? (
                <div style={{ color: data.change_pct > 0 ? '#ff4d4f' : '#52c41a', fontSize: 12, marginTop: 4 }}>
                  {data.change_pct > 0 ? <ArrowUpOutlined /> : <ArrowDownOutlined />} 环比 {Math.abs(data.change_pct).toFixed(1)}%
                </div>
              ) : null}
            </Card>
          </Col>
        ))}
      </Row>

      <Spin spinning={loading}>
        {data && data.history?.length ? (
          <Card style={{ borderRadius: 12 }}>
            <ReactECharts option={buildOption(data, data.threshold)} style={{ height: 340 }} notMerge />
          </Card>
        ) : (
          <AppEmpty description="暂无数据" tip="请确认 node-exporter 已采集资源指标" height={200} />
        )}
      </Spin>

      {data && data.history?.length ? (
        <>
          {/* B：算法局限免责提示（算法审核结论） */}
          <Alert
            type="info"
            showIcon
            style={{ margin: '16px 0' }}
            message="预测说明"
            description="预测采用线性回归 + EWMA 双算法，未做周期性分解，长周期外推误差可能放大，ETT 仅供参考，请勿作为唯一扩缩容依据。"
          />
          <Row gutter={[16, 16]}>
            <Col span={12}><ForecastCard title="线性回归预测" fc={data.forecasts.linear} isEwma={false} /></Col>
            <Col span={12}><ForecastCard title="EWMA 指数平滑预测" fc={data.forecasts.ewma} isEwma /></Col>
          </Row>
        </>
      ) : null}
    </div>
  )
}

export default Capacity
