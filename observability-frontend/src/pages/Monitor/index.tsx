import { useEffect, useState } from 'react'
import { Row, Col, Card, Spin, Button } from 'antd'
import AppEmpty from '../../components/AppEmpty'
import api from '../../api/client'

const PANELS = [
  { key: 'rate', title: '服务请求速率', query: 'sum(rate(http_requests_total[5m])) by (service)' },
  { key: 'error', title: '服务错误率', query: 'sum(rate(http_requests_total{status=~"5.."}[5m])) by (service)' },
  { key: 'p95', title: '延迟 P95', query: 'histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, service))' },
  { key: 'cpu', title: 'CPU 使用率', query: '100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100' },
]

const Monitor: React.FC = () => {
  const [rows, setRows] = useState<Record<string, any[]>>({})
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    const now = Math.floor(Date.now() / 1000)
    const start = now - 3600
    const step = '60'
    const results: Record<string, any[]> = {}
    await Promise.all(
      PANELS.map(async (p) => {
        try {
          const r = await api.get('/metrics/query_range', { params: { query: p.query, start, end: now, step } })
          results[p.key] = r?.data?.data?.result || []
        } catch {
          results[p.key] = []
        }
      }),
    )
    setRows(results)
    setLoading(false)
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <Spin spinning={loading}>
      <Row gutter={[16, 16]}>
        {PANELS.map((p) => (
          <Col span={12} key={p.key}>
            <Card
              title={p.title}
              extra={<Button size="small" onClick={load}>刷新</Button>}
              style={{ borderRadius: 12 }}
            >
              {rows[p.key]?.length ? (
                <pre style={{ color: 'var(--text-muted)', fontSize: 12, maxHeight: 200, overflow: 'auto' }}>
                  {JSON.stringify(rows[p.key], null, 2)}
                </pre>
              ) : (
                <AppEmpty description="暂无数据" tip="等待 VM 采集数据" height={80} />
              )}
            </Card>
          </Col>
        ))}
      </Row>
    </Spin>
  )
}

export default Monitor
