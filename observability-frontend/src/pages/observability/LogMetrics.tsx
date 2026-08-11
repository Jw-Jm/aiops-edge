import React, { useState, useEffect } from 'react'
import { Input, Button, Select, Space, Segmented, Tag, Table, Tooltip } from 'antd'
import { queryLogs, aggregateLogs } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

interface LogRow { ts: string; level: string; service_name: string; message: string; [k: string]: any }
interface AggRow { [k: string]: any; count?: number }

const LEVEL_TONE: Record<string, string> = { error: 'var(--danger)', warning: 'var(--warning)', info: 'var(--primary)', debug: 'var(--text-muted)' }

// 2.8 日志页重设计：数据源选择 + 级别过滤 + 时间范围（集群过滤由全局 ClusterSwitcher 注入）
const LogMetrics: React.FC = () => {
  const [mode, setMode] = useState<'logs' | 'aggregate'>('logs')
  const [source, setSource] = useState<'clickhouse' | 'victorialogs'>('clickhouse')
  const [level, setLevel] = useState<string>('all')
  const [hours, setHours] = useState<number>(24)
  const [q, setQ] = useState('')
  const [rows, setRows] = useState<LogRow[]>([])
  const [aggs, setAggs] = useState<AggRow[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  const search = (targetMode?: 'logs' | 'aggregate') => {
    const m = targetMode || mode
    setLoading(true)
    setErr('')
    const p: Record<string, unknown> = {
      limit: 100,
      source,
      hours,
      ...(q ? { query: q } : {}),
      ...(level !== 'all' ? { level } : {}),
    }
    const req = m === 'logs' ? queryLogs(p) : aggregateLogs({ ...p, group_by: 'service_name' })
    req.then((r) => {
      const d = r.data
      if (m === 'logs') {
        // Issue3/5: 统一 clickhouse 与 victorialogs 字段（query-api 已归一为 body/service_name/severity/timestamp）
        // _source 直接取当前选中的 source（用户选择的 VictoriaLogs 即标记为 VictoriaLogs）
        const raw: any[] = Array.isArray(d) ? d : d?.data || d?.rows || []
        setRows(raw.map((x: any) => ({
          ...x,
          ts: x.ts || x.timestamp || x._time || '',
          level: x.level || x.severity || 'info',
          service_name: x.service_name || x.service || x.kubernetes?.container_name || '-',
          message: x.message || x.body || x._msg || '',
          pod: x.pod || x.kubernetes?.pod_name || x.namespace || '',
          _source: source === 'victorialogs' ? 'victorialogs' : 'clickhouse',
        })))
      } else {
        // Issue5: 后端聚合返回 { services: [{service,count}], trend, levels } 对象而非数组；
        // 取 services 并映射 service→service_name 供表格展示
        const svc = Array.isArray(d) ? d : (d?.services || d?.data || d?.rows || [])
        setAggs(svc.map((x: any) => ({ ...x, service_name: x.service_name || x.service, count: Number(x.count ?? x.cnt ?? 0) })))
      }
    }).catch((e) => {
      setErr(e?.response?.data?.error || '查询失败')
      setRows([]); setAggs([])
    }).finally(() => setLoading(false))
  }

  // P3-2 首次加载自动查询
  useEffect(() => { search() }, [])

  const logCols = [
    { title: '时间', dataIndex: 'ts', key: 'ts', render: (v: string) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>{v}</span>, width: 165 },
    { title: '级别', dataIndex: 'level', key: 'level', width: 70, render: (v: string) => <Tag color="default" style={{ color: LEVEL_TONE[v] || 'var(--text-muted)', fontWeight: 500 }}>{v || '-'}</Tag> },
    { title: '服务', dataIndex: 'service_name', key: 'service_name', width: 170 },
    { title: '来源', dataIndex: '_source', key: '_source', width: 90, render: (v: string) => v === 'victorialogs' ? <Tag color="purple" style={{ margin: 0 }}>VictoriaLogs</Tag> : <Tag color="blue" style={{ margin: 0 }}>ClickHouse</Tag> },
    { title: '消息', dataIndex: 'message', key: 'message', ellipsis: true },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: '日志与指标' }]} />
      <PageHeader title="日志与指标" desc="跨数据源检索日志 / 按服务聚合统计" />

      <div className="card" style={{ padding: 16 }}>
        <Space wrap style={{ width: '100%' }}>
          <Segmented value={mode} onChange={(v) => { const m = v as any; setMode(m); setRows([]); setAggs([]); search(m) }}
            options={[{ label: '日志检索', value: 'logs' }, { label: '聚合统计', value: 'aggregate' }]} />
          <Tooltip title="ClickHouse 为平台默认日志存储；VictoriaLogs 可选">
            <Select value={source} onChange={(v) => setSource(v as any)} style={{ width: 140 }}
              options={[{ value: 'clickhouse', label: '数据源 · ClickHouse' }, { value: 'victorialogs', label: '数据源 · VictoriaLogs' }]} />
          </Tooltip>
          <Select value={level} onChange={setLevel} style={{ width: 100 }}
            options={[{ value: 'all', label: '全部级别' }, { value: 'error', label: 'error' }, { value: 'warning', label: 'warning' }, { value: 'info', label: 'info' }, { value: 'debug', label: 'debug' }]} />
          <Select value={hours} onChange={setHours} style={{ width: 100 }}
            options={[{ value: 1, label: '近 1 小时' }, { value: 6, label: '近 6 小时' }, { value: 24, label: '近 24 小时' }, { value: 168, label: '近 7 天' }]} />
          <Input value={q} onChange={(e) => setQ(e.target.value)} onPressEnter={() => search()} placeholder="搜索关键词，如 error / 服务名" style={{ width: 360 }} />
          <Button type="primary" onClick={() => search()} loading={loading}>查询</Button>
        </Space>
        {err && <div style={{ marginTop: 10, color: 'var(--danger)', fontSize: 12 }}>⚠ {err}</div>}
      </div>

      <div className="card" style={{ padding: 0 }}>
        {mode === 'logs' ? (
          <Table rowKey={(r) => `${r.ts}-${r.message}`} loading={loading} columns={logCols} dataSource={rows}
            size="small" pagination={{ pageSize: 20 }} scroll={{ x: 900, y: 'calc(100vh - 360px)' }} locale={{ emptyText: <Empty text="暂无日志" /> }} />
        ) : (
          <Table rowKey={(r) => r.service_name || r._group} loading={loading}
            columns={[{ title: '服务', dataIndex: 'service_name', key: 'service_name' },
              { title: '条数', dataIndex: 'count', key: 'count' }]}
            dataSource={aggs} size="small" pagination={false} locale={{ emptyText: <Empty text="暂无聚合结果" /> }} />
        )}
      </div>
    </div>
  )
}

export default LogMetrics
