import React, { useState, useEffect, useCallback } from 'react'
import { Card, Input, Button, Table, Tag, Space, Typography, Select, message, Segmented, Row, Col, Badge } from 'antd'
import { SearchOutlined, ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons'
import api from '../../api/client'
import { queryLogs } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const { Text } = Typography

interface LogEntry {
  id: string; timestamp: string; service: string; severity: string; body: string; trace_id?: string
}

const SEVERITY_COLORS: Record<string, string> = {
  DEBUG: 'default', INFO: 'blue', WARN: 'orange', WARNING: 'orange', ERROR: 'red', FATAL: 'magenta', CRITICAL: 'magenta',
}

const TIME_PRESETS = [
  { label: '15m', value: 15 }, { label: '1h', value: 60 }, { label: '6h', value: 360 }, { label: '24h', value: 1440 },
]

const SEVERITY_FILTERS = [
  { label: '全部', value: '' }, { label: 'ERROR', value: 'error' }, { label: 'WARN', value: 'warn' },
  { label: 'INFO', value: 'info' }, { label: 'DEBUG', value: 'debug' },
]

const Logs: React.FC = () => {
  const [backend, setBackend] = useState<'victorialogs' | 'clickhouse'>('victorialogs')
  const [service, setService] = useState('')
  const [keyword, setKeyword] = useState('')
  const [logsQuery, setLogsQuery] = useState('_time:15m')
  const [timePreset, setTimePreset] = useState(15) // minutes
  const [severityFilter, setSeverityFilter] = useState('')
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [vlStatus, setVlStatus] = useState<'ok' | 'err' | 'checking'>('checking')

  // Check VictoriaLogs status
  useEffect(() => { checkVLStatus() }, [])
  const checkVLStatus = async () => {
    try {
      const r = await api.get('/logs/victorialogs', { params: { query: '_time:1m', limit: 1 }, timeout: 5000 })
      setVlStatus(r.status === 200 ? 'ok' : 'err')
    } catch { setVlStatus('err') }
  }

  const setTimeRangePreset = (minutes: number) => {
    // 只更新时间预设，不改写用户输入的查询词（避免破坏用户手写的 LogsQL）
    setTimePreset(minutes)
  }

  const fetchLogs = useCallback(async () => {
    setLoading(true)
    try {
      if (backend === 'victorialogs') {
        // 时间/级别/搜索词结构化组合（时间预设与级别过滤不再改写用户输入的 logsQuery，
        // 避免状态错乱与非法 LogsQL）。用户输入优先，预设作为默认时间兜底。
        let q = logsQuery.trim()
        if (!q.includes('_time:')) q = `_time:${timePreset}m${q ? ' ' + q : ''}`
        else if (severityFilter && !q.includes(severityFilter)) q = `${q} ${severityFilter}`
        else if (severityFilter) q = `${q} ${severityFilter}`
        // 注意：axios 会对 params 自动 URL 编码，这里不要再手动 encodeURIComponent，
        // 否则特殊字符（%、+、&）会被双重编码（% → %25）导致查询错误。
        const res = await api.get('/logs/victorialogs', {
          params: { query: q, limit: 100 },
          timeout: 15000,
        })
        // VL returns JSONL (one JSON per line), parse accordingly
        let rawData = res.data;
        if (typeof rawData === 'string') {
          rawData = rawData.trim().split('\n').filter(Boolean).map((l: string) => { try { return JSON.parse(l) } catch { return null } }).filter(Boolean);
        }
        const data = Array.isArray(rawData) ? rawData : rawData?.hits || rawData?._values || []
        const mapped = data.map((l: any) => ({
          id: l._id || l._stream || Math.random().toString(36),
          timestamp: l._time || l.timestamp || '',
          service: l.service || l.service_name || l._stream || l.app || l.pod || '',
          severity: l.severity || l.level || l._stream_level || 'INFO',
          body: l._msg || l.body || l.message || l.msg || JSON.stringify(l),
          trace_id: l.trace_id || l.traceId,
        }))
        setLogs(mapped)
        if (mapped.length > 0) setVlStatus('ok')
      } else {
        const params: Record<string, unknown> = {}
        if (service.trim()) params.service = service.trim()
        if (keyword.trim()) params.query = keyword.trim()
        const res = await queryLogs(params)
        const data = res.data?.data || res.data || []
        const arr = Array.isArray(data) ? data : (data?.logs || [])
        // 字段归一化：后端返回 service_name/body/timestamp/severity/trace_id
        const mapped = arr.map((l: any) => ({
          id: l.id || l.trace_id || l.traceId || Math.random().toString(36),
          timestamp: l.timestamp || l._time || '',
          service: l.service || l.service_name || l._stream || l.app || l.pod || '',
          severity: l.severity || l.level || 'INFO',
          body: l.body || l.message || l.msg || JSON.stringify(l),
          trace_id: l.trace_id || l.traceId,
        }))
        setLogs(mapped)
      }
    } catch { message.error('查询日志失败') }
    setLoading(false)
  }, [backend, service, keyword, logsQuery, severityFilter])

  useEffect(() => { fetchLogs() }, [])

  const columns = [
    { title: '时间', dataIndex: 'timestamp', key: 'ts', width: 170,
      render: (v: string) => <Text style={{ fontSize: 12, fontFamily: 'monospace', whiteSpace: 'nowrap', color: 'var(--text-muted)' }}>{fmtLocalTime(v, '-', 'MM-DD HH:mm:ss')}</Text> },
    { title: '服务', dataIndex: 'service', key: 'svc', width: 200, ellipsis: true,
      render: (v: string) => v ? <Text code style={{ fontSize: 12, whiteSpace: 'nowrap' }}>{v}</Text> : '-' },
    { title: '级别', dataIndex: 'severity', key: 'lvl', width: 80,
      render: (v: string) => { const c = SEVERITY_COLORS[v?.toUpperCase()] || 'default'; return v ? <Tag color={c} style={{ margin: 0 }}>{v.toUpperCase()}</Tag> : '-' } },
    { title: '内容', dataIndex: 'body', key: 'body', ellipsis: true,
      render: (v: string, record: LogEntry) => (
        <Text style={{ fontSize: 12, fontFamily: 'SF Mono, Monaco, monospace', wordBreak: 'break-all', cursor: 'pointer' }}
          onClick={() => message.info({ content: v, duration: 8, style: { maxWidth: 800 } })}>
          {v?.length > 150 ? v.slice(0, 150) + '...' : v || '-'}
        </Text>),
    },
  ]

  return (
    <div>
      {/* Search bar */}
      <Card size='small' style={{ marginBottom: 12, background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
        <Space direction='vertical' size='small' style={{ width: '100%' }}>
          <Row gutter={[12, 8]} align='middle'>
            <Col>
              <Select value={backend} onChange={(v) => setBackend(v)}
                options={[{ value: 'victorialogs', label: 'VictoriaLogs' }, { value: 'clickhouse', label: 'ClickHouse' }]}
                style={{ width: 140 }} />
            </Col>
            <Col flex='auto'>
              {backend === 'victorialogs' ? (
                <Input prefix={<SearchOutlined />} placeholder='LogsQL: _time:15m error | {app="myapp"}'
                  value={logsQuery} onChange={e => setLogsQuery(e.target.value)} onPressEnter={fetchLogs} allowClear style={{ fontFamily: 'monospace' }} />
              ) : (
                <Space.Compact style={{ width: '100%' }}>
                  <Input placeholder='服务' value={service} onChange={e => setService(e.target.value)} style={{ width: 180 }} allowClear />
                  <Input placeholder='关键词' value={keyword} onChange={e => setKeyword(e.target.value)} style={{ width: 250 }} allowClear onPressEnter={fetchLogs} />
                </Space.Compact>
              )}
            </Col>
            <Col>
              <Space size={4}>
                {backend === 'victorialogs' && (
                  <>
                    {TIME_PRESETS.map(t => (
                      <Button key={t.value} size='small' type={timePreset === t.value ? 'primary' : 'default'}
                        onClick={() => setTimeRangePreset(t.value)}>{t.label}</Button>
                    ))}
                    {/* 级别过滤为独立状态，由 fetchLogs 结构化组合，不改写用户输入的查询词 */}
                    <Select size='small' value={severityFilter} onChange={v => setSeverityFilter(v)}
                      options={SEVERITY_FILTERS} style={{ width: 80 }} />
                  </>
                )}
                <Badge status={vlStatus === 'ok' ? 'success' : vlStatus === 'err' ? 'error' : 'processing'} text={backend === 'victorialogs' ? 'VL' : ''} />
                <Button type='primary' icon={<SearchOutlined />} onClick={fetchLogs} loading={loading} size='small'>查询</Button>
                <Button icon={<ReloadOutlined />} onClick={fetchLogs} size='small' />
              </Space>
            </Col>
          </Row>
          {backend === 'victorialogs' && (
            <Row>
              <Col>
                <Text style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                  <ThunderboltOutlined /> LogsQL: <Text code style={{ fontSize: 11 }}>_time:15m error</Text> 按时间/关键词搜 |
                  <Text code style={{ fontSize: 11 }}>{'{service="api"}'}</Text> 按标签过滤 |
                  <Text code style={{ fontSize: 11 }}>| stats count() by (level)</Text> 聚合统计
                </Text>
              </Col>
            </Row>
          )}
        </Space>
      </Card>

      {/* Results */}
      <Card size='small' title={logs.length > 0 ? `日志 (${logs.length} 条)` : '日志'} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
        <Table columns={columns} dataSource={logs} rowKey={r => r.id}
          loading={loading} pagination={{ pageSize: 25, showSizeChanger: true, showTotal: t => `共 ${t} 条` }}
          size='small' scroll={{ x: 800 }}
          locale={{ emptyText: loading ? '查询中...' : backend === 'victorialogs' ? '暂无日志 — 确认 VictoriaLogs 已收到数据' : '暂无日志' }}
          expandable={{
            expandedRowRender: (r: LogEntry) => (
              <div style={{ padding: 8 }}>
                <Text copyable style={{ fontSize: 12, fontFamily: 'monospace', whiteSpace: 'pre-wrap' }}>{r.body}</Text>
                {r.trace_id && <div style={{ marginTop: 4 }}><Text type='secondary' style={{ fontSize: 11 }}>TraceID: </Text><Text copyable code style={{ fontSize: 11 }}>{r.trace_id}</Text></div>}
              </div>
            ),
            rowExpandable: (r: LogEntry) => (r.body?.length || 0) > 100,
          }}
        />
      </Card>
    </div>
  )
}

export default Logs
