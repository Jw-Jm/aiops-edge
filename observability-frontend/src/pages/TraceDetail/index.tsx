import React, { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Card, Spin, Timeline, Tag, Empty, Tabs, Table, Alert } from 'antd'
import { getTraceDetail, getTraceContext } from '../../api/client'
import { fmtLocalTime, fmtLocalMs } from '../../utils/date'

interface Span {
  span_id: string; parent_span_id: string; service_name: string
  operation_name: string; span_kind: string; start_time: string; ms: number; is_error: number
}

const TraceDetail: React.FC = () => {
  const { traceId } = useParams<{ traceId: string }>()
  const [spans, setSpans] = useState<Span[]>([])
  const [loading, setLoading] = useState(true)
  // 关联证据（数据血缘闭环）
  const [ctx, setCtx] = useState<any>(null)
  const [ctxLoading, setCtxLoading] = useState(false)

  useEffect(() => {
    if (!traceId) return
    getTraceDetail(traceId).then(r => {
      // 后端返回 { count, spans, trace_id } 或 { data: [...] }
      const d = r.data
      setSpans(Array.isArray(d) ? d : (Array.isArray(d?.spans) ? d.spans : (Array.isArray(d?.data) ? d.data : [])))
    }).finally(() => setLoading(false))
    // 加载关联证据
    setCtxLoading(true)
    getTraceContext(traceId).then(r => {
      setCtx(r.data?.data || r.data || null)
    }).catch(() => setCtx(null)).finally(() => setCtxLoading(false))
  }, [traceId])

  const kindColor: Record<string, string> = { SERVER: 'blue', CLIENT: 'green', PRODUCER: 'purple', CONSUMER: 'orange', INTERNAL: 'default' }

  // 关联证据 Tab
  const evidenceTabs = [
    {
      key: 'logs', label: `关联日志 (${ctx?.logs?.length || 0})`,
      children: (
        <Table
          size="small" rowKey={(r: any, i?: number) => String(i)} pagination={{ pageSize: 8 }}
          dataSource={ctx?.logs || []} locale={{ emptyText: '该 trace 无关联日志' }}
          columns={[
            { title: '时间', dataIndex: 'timestamp', width: 170, render: (v: string) => fmtLocalTime(v, '-', 'MM-DD HH:mm:ss') },
            { title: '服务', dataIndex: 'service_name', width: 140, render: (v: string) => <Tag>{v || '-'}</Tag> },
            { title: '级别', dataIndex: 'severity', width: 90, render: (v: string) => <Tag color={v === 'ERROR' ? 'red' : v === 'WARN' ? 'orange' : 'default'}>{v || '-'}</Tag> },
            { title: '内容', dataIndex: 'body', ellipsis: true },
          ]}
        />
      ),
    },
    {
      key: 'vlogs', label: `VictoriaLogs (${ctx?.vlogs?.length || 0})`,
      children: (
        <Table
          size="small" rowKey={(r: any, i?: number) => String(i)} pagination={{ pageSize: 8 }}
          dataSource={ctx?.vlogs || []} locale={{ emptyText: 'VictoriaLogs 无匹配日志' }}
          columns={[
            { title: '时间', dataIndex: '_time', width: 170, render: (v: string) => fmtLocalTime(v, '-', 'MM-DD HH:mm:ss') },
            { title: '服务', dataIndex: 'pod', width: 180, render: (v: string) => <Tag>{v || '-'}</Tag> },
            { title: '内容', dataIndex: '_msg', ellipsis: true },
          ]}
        />
      ),
    },
    {
      key: 'metrics', label: `服务指标 (${ctx?.metrics?.length || 0})`,
      children: (
        <Table
          size="small" rowKey={(r: any, i?: number) => String(i)} pagination={{ pageSize: 8 }}
          dataSource={ctx?.metrics || []} locale={{ emptyText: '近 30 分钟无指标' }}
          columns={[
            { title: '时间', dataIndex: 't', width: 170, render: (v: string) => fmtLocalTime(v, '-', 'MM-DD HH:mm') },
            { title: '调用量', dataIndex: 'call_count', width: 100 },
            { title: '错误数', dataIndex: 'error_count', width: 100, render: (v: number) => <Tag color={v > 0 ? 'red' : 'green'}>{v || 0}</Tag> },
            { title: '平均耗时(ms)', dataIndex: 'avg_ms', render: (v: number) => (v ?? 0).toFixed(1) },
          ]}
        />
      ),
    },
    {
      key: 'alerts', label: `关联告警 (${ctx?.alerts?.length || 0})`,
      children: (
        <Table
          size="small" rowKey={(r: any, i?: number) => String(i)} pagination={{ pageSize: 8 }}
          dataSource={ctx?.alerts || []} locale={{ emptyText: '近 30 分钟无关联告警' }}
          columns={[
            { title: '规则', dataIndex: 'rule_name', ellipsis: true },
            { title: '级别', dataIndex: 'severity', width: 90, render: (v: string) => <Tag color={v === 'critical' ? 'red' : v === 'warning' ? 'orange' : 'default'}>{v || '-'}</Tag> },
            { title: '次数', dataIndex: 'count', width: 70, render: (v: number) => v || 0 },
            { title: '内容', dataIndex: 'message', ellipsis: true },
          ]}
        />
      ),
    },
  ]

  if (!loading && spans.length === 0) return <Empty description="Trace 未找到" />

  return (
    <Spin spinning={loading}>
      <Card title={<span style={{ fontFamily: 'monospace' }}>Trace: {(traceId || '').slice(0, 16)}...</span>}>
        <h4 style={{ marginBottom: 16 }}>Span 瀑布图 ({spans.length} spans)</h4>
        <div style={{ overflowX: 'auto' }}>
          {spans.map((s, i) => {
            const indent = spans.filter(other => other.span_id === s.parent_span_id).length > 0 ? 24 : 0
            return (
              <div key={s.span_id || i} style={{ display: 'flex', alignItems: 'center', padding: '4px 0', borderBottom: '1px solid #f0f0f0' }}>
                <div style={{ width: 50, textAlign: 'right', paddingRight: 8, flexShrink: 0 }}>
                  <span style={{ color: '#999', fontSize: 12 }}>{s.ms?.toFixed(1)}ms</span>
                </div>
                <div style={{ marginLeft: indent, flex: 1, display: 'flex', alignItems: 'center', gap: 8 }}>
                  <div style={{ width: Math.max(20, Math.min(s.ms || 1, 800) / 4), height: 20, background: s.is_error ? '#ff4d4f' : 'var(--primary)', borderRadius: 4, opacity: 0.8, flexShrink: 0 }} />
                  <span style={{ fontFamily: 'monospace', fontSize: 13 }}>{s.service_name}</span>
                  <span style={{ color: 'var(--text-muted)', fontSize: 13 }}>{s.operation_name}</span>
                  <Tag color={kindColor[s.span_kind] || 'default'} style={{ fontSize: 11 }}>{s.span_kind}</Tag>
                </div>
                <div style={{ width: 160, textAlign: 'right', color: '#999', fontSize: 12, flexShrink: 0 }}>
                  {fmtLocalMs(s.start_time)}
                </div>
              </div>
            )
          })}
        </div>

        {/* 关联证据（数据血缘闭环） */}
        <div style={{ marginTop: 24 }}>
          <h4 style={{ marginBottom: 8 }}>关联证据（Trace → 日志 / 指标 / 告警）</h4>
          <Spin spinning={ctxLoading}>
            <Tabs items={evidenceTabs} />
          </Spin>
        </div>
      </Card>
    </Spin>
  )
}

export default TraceDetail
