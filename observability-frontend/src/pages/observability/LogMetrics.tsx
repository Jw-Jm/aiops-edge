import React, { useState, useEffect, useRef } from 'react'
import { Input, Button, Select, Space, Segmented, Tag, Table, Tooltip } from 'antd'
import { queryLogs, aggregateLogs } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

interface LogRow { ts: string; level: string; service_name: string; message: string; [k: string]: any }
interface AggRow { [k: string]: any; count?: number }

const LEVEL_TONE: Record<string, string> = { error: 'var(--danger)', warning: 'var(--warning)', info: 'var(--primary)', debug: 'var(--text-muted)' }

// 2.8 日志页重设计：数据源选择 + 级别过滤 + 时间范围（集群过滤由全局 ClusterSwitcher 注入）
const LogMetrics: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const [mode, setMode] = useState<'logs' | 'aggregate'>('logs')
  // Raw Logs SoT is VictoriaLogs in the production reader mode. ClickHouse
  // remains the derived-analytics store and is intentionally not exposed as
  // an empty raw-log option when its log_records table is not configured.
  const source = 'victorialogs' as const
  const [level, setLevel] = useState<string>('all')
  const [hours, setHours] = useState<number>(24)
  // 修复(P2-3)：默认过滤健康检查噪音日志（/health、/ready、/v1/query 等探针请求），
  // 否则日志列表被海量 /health [200] 0ms 淹没，用户看不到真实业务日志。
  const [hideHealth, setHideHealth] = useState<boolean>(true)
  const [q, setQ] = useState('')
  const [rows, setRows] = useState<LogRow[]>([])
  const [aggs, setAggs] = useState<AggRow[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const requestSeq = useRef(0)

  // A7: 筛选条件变更时自动触发查询（与模式切换一致）。overrides 让 onChange 立即生效，
  // 避免 setState 异步导致 search() 读到旧值。
  const search = (targetMode?: 'logs' | 'aggregate', overrides: Partial<{ source: string; level: string; hours: number; hideHealth: boolean }> = {}) => {
    const requestId = ++requestSeq.current
    const m = targetMode || mode
    setLoading(true)
    setErr('')
    const src = overrides.source ?? source
    const lv = overrides.level ?? level
    const hr = overrides.hours ?? hours
    const hh = overrides.hideHealth ?? hideHealth
    const p: Record<string, unknown> = {
      limit: 100,
      source: src,
      hours: hr,
      ...(q ? { query: q } : {}),
      ...(lv !== 'all' ? { level: lv } : {}),
      // 修复(P2-3)：过滤健康检查探针日志（/health、/ready、/v1/query）
      ...(hh ? { exclude_health: true } : {}),
    }
    const req = m === 'logs' ? queryLogs(p) : aggregateLogs({ ...p, group_by: 'service_name' })
    req.then((r) => {
      if (requestId !== requestSeq.current) return
      const d = r.data
      if (m === 'logs') {
        // Issue3/5: 统一 clickhouse 与 victorialogs 字段（query-api 已归一为 body/service_name/severity/timestamp）
        // A7: _source 读取返回数据行自身的来源（x.source / x._source，VictoriaLogs 行自带 source 字段），
        // 不再直接取当前选中的 source，避免显示标签与实际数据源不符。
        const raw: any[] = Array.isArray(d) ? d : d?.data || d?.rows || []
        const respSource = (d as any)?.source || src
        const normalizedRows = raw.map((x: any) => ({
          ...x,
          ts: x.ts || x.timestamp || x._time || '',
          // 不把缺失的真实级别伪装成 info；VictoriaLogs 的非结构化日志可能没有级别字段。
          level: x.level || x.severity || 'unknown',
          service_name: x.service_name || x.service || x.kubernetes?.container_name || '-',
          message: x.message || x.body || x._msg || '',
          pod: x.pod || x.kubernetes?.pod_name || x.namespace || '',
          _source: x._source || x.source || respSource,
        }))
        // VictoriaLogs can return unstructured rows even when the LogsQL field
        // predicate is present. Keep the UI projection strict as a second
        // guard so a selected level never displays an unknown/non-matching row.
        setRows(lv === 'all'
          ? normalizedRows
          : normalizedRows.filter((row) => typeof row.level === 'string' && row.level.toLowerCase() === lv.toLowerCase()))
      } else {
        // Issue5: 后端聚合返回 { services: [{service,count}], trend, levels } 对象而非数组；
        // 取 services 并映射 service→service_name 供表格展示
        const svc = Array.isArray(d) ? d : (d?.services || d?.data || d?.rows || [])
        setAggs(svc.map((x: any) => ({ ...x, service_name: x.service_name || x.service, count: Number(x.count ?? x.cnt ?? 0) })))
      }
    }).catch((e: any) => {
      if (requestId !== requestSeq.current) return
      setErr(e?.response?.data?.error || '查询失败')
      setRows([]); setAggs([])
    }).finally(() => {
      if (requestId === requestSeq.current) setLoading(false)
    })
  }

  // P3-2 首次加载自动查询
  useEffect(() => { search() }, [currentClusterId])

  const logCols = [
    { title: '时间', dataIndex: 'ts', key: 'ts', render: (v: string) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>{v}</span>, width: 165 },
    { title: '级别', dataIndex: 'level', key: 'level', width: 70, render: (v: string) => <Tag color="default" style={{ color: LEVEL_TONE[v] || 'var(--text-muted)', fontWeight: 500 }}>{v === 'unknown' || !v ? '未知' : v}</Tag> },
    { title: '服务', dataIndex: 'service_name', key: 'service_name', width: 170 },
    { title: '来源', dataIndex: '_source', key: '_source', width: 90, render: (v: string) => v === 'victorialogs' ? <Tag color="purple" style={{ margin: 0 }}>VictoriaLogs</Tag> : <Tag color="blue" style={{ margin: 0 }}>ClickHouse</Tag> },
    { title: '消息', dataIndex: 'message', key: 'message', ellipsis: true },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: '日志与指标' }]} />
      <PageHeader title="日志与指标" desc="原始日志（SoT）与异常模式（derived analytics）分开展示" />

      <div className="card" style={{ padding: 16 }}>
        <Space wrap style={{ width: '100%' }}>
          <Segmented value={mode} onChange={(v) => { const m = v as any; setMode(m); setRows([]); setAggs([]); search(m) }}
            options={[{ label: '原始日志', value: 'logs' }, { label: '异常模式', value: 'aggregate' }]} />
          <Tag color="purple" title="原始日志真实来源">数据源 · VictoriaLogs</Tag>
          {/* A7: 级别/时间范围/探针过滤变更自动重新查询 */}
          <Select value={level} onChange={(v) => { const nl = v as string; setLevel(nl); search(undefined, { level: nl }) }} style={{ width: 100 }}
            options={[{ value: 'all', label: '全部级别' }, { value: 'error', label: '错误' }, { value: 'warning', label: '警告' }, { value: 'info', label: '信息' }, { value: 'debug', label: '调试' }]} />
          <Select value={hours} onChange={(v) => { const nh = v as number; setHours(nh); search(undefined, { hours: nh }) }} style={{ width: 100 }}
            options={[{ value: 1, label: '近 1 小时' }, { value: 6, label: '近 6 小时' }, { value: 24, label: '近 24 小时' }, { value: 168, label: '近 7 天' }]} />
          <Input value={q} onChange={(e) => setQ(e.target.value)} onPressEnter={() => search()} placeholder="搜索关键词，如 error / 服务名" style={{ width: 320 }} />
          <Button type={hideHealth ? 'default' : 'primary'} onClick={() => { const nhh = !hideHealth; setHideHealth(nhh); search(undefined, { hideHealth: nhh }) }} title="过滤 /health、/v1/query 等探针噪音日志">{hideHealth ? '过滤探针' : '显示探针'}</Button>
          <Button type="primary" onClick={() => search()} loading={loading}>查询</Button>
        </Space>
        {err && <div style={{ marginTop: 10, color: 'var(--danger)', fontSize: 12 }}>⚠ {err}</div>}
      </div>

      <div className="card" style={{ padding: 0 }}>
        {mode === 'logs' ? (
          <Table rowKey={(r) => `${r.ts}-${r.level}-${r.service_name}-${(r.message || '').slice(0, 20)}`} loading={loading} columns={logCols} dataSource={rows}
            size="small" pagination={{ pageSize: 20 }} scroll={{ x: 900, y: 'calc(100vh - 360px)' }} locale={{ emptyText: <Empty text="暂无日志" hint="试试切换数据源或扩大时间范围" /> }} />
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
