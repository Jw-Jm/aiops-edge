import React, { useEffect, useState } from 'react'
import { Drawer, Spin, Table, Tag, Select, Space, Button, Input } from 'antd'
import { getTraces, getTraceDetail, getTraceContext } from '../../api/client'
import { useUIStore } from '../../store/uiStore'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

interface TraceRow { trace_id: string; services?: any; max_ms?: number; spans?: number; start?: string; end?: string }

interface SpanNode { span: any; depth: number }

// 2.6 树形瀑布图：按 parent_span_id 构建 span 树，输出 (树根列表, 最大耗时)
function buildSpanTree(spans: any[]): { roots: SpanNode[]; maxMs: number } {
  const map = new Map<string, any>()
  const childrenOf = new Map<string, any[]>()
  for (const s of spans || []) {
    map.set(s.span_id, s)
    if (!childrenOf.has(s.parent_span_id)) childrenOf.set(s.parent_span_id, [])
    childrenOf.get(s.parent_span_id)!.push(s)
  }
  const roots: SpanNode[] = []
  const maxMs = Math.max(1, ...(spans || []).map((s) => Number(s.ms) || 0))
  const walk = (parentId: string, depth: number) => {
    const kids = childrenOf.get(parentId) || []
    // 按开始时间排序，保证时间轴顺序
    kids.sort((a, b) => (a.start_time || 0) - (b.start_time || 0))
    for (const k of kids) {
      roots.push({ span: k, depth })
      walk(k.span_id, depth + 1)
    }
  }
  // 根 = parent 不存在于当前 spans（孤儿）或 parent 为空的 span
  const ids = new Set(spans?.map((s) => s.span_id) || [])
  const rootIds = new Set(spans?.filter((s) => !s.parent_span_id || !ids.has(s.parent_span_id)).map((s) => s.span_id) || [])
  if (rootIds.size === 0 && spans?.length) rootIds.add(spans[0].span_id)
  // 修复(P1 链路追踪详情)：先 push root span 本身（带 depth 0），再递归走 children。
  // 之前直接 walk(rid, 0) 不会把 root 加入 roots，导致客户端/根 span 丢失，
  // 详情页只显示服务端 span，看起来像"少了一个 span"。
  for (const rid of rootIds) {
    const root = map.get(rid)
    if (root) roots.push({ span: root, depth: 0 })
    walk(rid, 0)
  }
  return { roots, maxMs }
}

const Trace: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const [data, setData] = useState<TraceRow[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<any>(null)
  const [ctx, setCtx] = useState<any>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [svc, setSvc] = useState<string>('')
  const [search, setSearch] = useState<string>('')
  const [limit, setLimit] = useState<number>(50)

  const load = (s = svc, l = limit, q = search) => {
    setLoading(true)
    getTraces({ limit: l, service_name: s || undefined, search: q || undefined })
      .then((r) => setData(Array.isArray(r.data) ? r.data : r.data?.data || []))
      .catch(() => setData([]))
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [currentClusterId])

  const openDetail = (id: string) => {
    setDrawerOpen(true)
    setDrawerLoading(true)
    setDetail(null); setCtx(null)
    Promise.all([getTraceDetail(id), getTraceContext(id)])
      .then(([d, c]) => { setDetail(d.data); setCtx(c.data) })
      .catch(() => setDetail({}))
      .finally(() => setDrawerLoading(false))
  }

  const cols = [
    { title: 'Trace ID', dataIndex: 'trace_id', key: 'trace_id', width: 220, render: (v: string) => <a onClick={() => openDetail(v)} style={{ fontFamily: 'var(--font-mono)', color: 'var(--primary)', fontSize: 12 }}>{(v || '').slice(0, 24)}</a> },
    { title: '服务数', dataIndex: 'services', key: 'services', render: (v: any) => <span style={{ fontSize: 12 }}>{v ?? '-'}</span> },
    { title: '最大延迟', dataIndex: 'max_ms', key: 'max_ms', render: (v: number) => v != null ? `${v.toFixed(2)}ms` : '-', sorter: (a: TraceRow, b: TraceRow) => (a.max_ms || 0) - (b.max_ms || 0) },
    { title: 'Spans', dataIndex: 'spans', key: 'spans', width: 80 },
    { title: '开始时间', dataIndex: 'start', key: 'start', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
  ]

  // 2.6 树形瀑布图
  const { roots: spanTree, maxMs } = buildSpanTree(detail?.spans || [])
  const spanStartBase = spanTree.length ? spanTree[0].span?.start_time || 0 : 0

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: '链路追踪' }]} />
      <PageHeader title="链路追踪" desc="按服务检索分布式调用链，下钻到单条 Trace 瀑布图"
        actions={<Space wrap>
          <Input.Search allowClear placeholder="搜索 trace_id / 操作 / URL" style={{ width: 240 }} value={search}
            onChange={(e) => setSearch(e.target.value)}
            onSearch={(q) => { setSearch(q); load(svc, limit, q) }} />
          <Select value={limit} onChange={(v) => { setLimit(v); load(svc, v) }} style={{ width: 100 }} options={[20, 50, 100].map((n) => ({ value: n, label: `${n} 条` }))} />
          <Button onClick={() => load()}>刷新</Button></Space>} />

      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="trace_id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={{ pageSize: 20 }} scroll={{ x: 800 }}
          locale={{ emptyText: <Empty text="暂无调用链数据" hint="请确认服务已上报 trace，或尝试调整时间范围" /> }} />
      </div>

      <Drawer width={620} open={drawerOpen} onClose={() => setDrawerOpen(false)} destroyOnClose title={`Trace ${detail?.trace_id || ''}`}
        styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        {drawerLoading ? <div style={{ textAlign: 'center', padding: 60 }}><Spin /></div> : (
          <div>
            <div style={{ marginBottom: 16, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              <Tag color="blue">Spans {(detail?.spans || []).length}</Tag>
              {Array.from(new Set((detail?.spans || []).map((s: any) => s.service_name).filter(Boolean))).slice(0, 5).map((s: any) => (
                <Tag key={s}>{s}</Tag>
              ))}
              <Tag color={(detail?.spans || []).some((s: any) => s.is_error) ? 'red' : 'green'}>
                {(detail?.spans || []).some((s: any) => s.is_error) ? '含错误' : '正常'}
              </Tag>
            </div>
            {/* 2.6 树形瀑布图：按 parent_span_id 缩进层级 + 时间轴比例 */}
            {spanTree.length > 0 && (
              <div style={{ marginBottom: 12, border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden', background: 'var(--surface-2)' }}>
                <div style={{ padding: '6px 12px', borderBottom: '1px solid var(--border)', fontSize: 12, color: 'var(--text-muted)', display: 'flex', justifyContent: 'space-between' }}>
                  <span>耗时</span><span>相对时间轴（总 {maxMs.toFixed(1)}ms）</span>
                </div>
                {spanTree.map(({ span: s, depth }, i) => (
                  <div key={s.span_id || i} style={{ display: 'flex', alignItems: 'center', padding: '4px 12px', borderBottom: i < spanTree.length - 1 ? '1px solid var(--border-soft)' : 'none' }}>
                    <span style={{ width: 52, flexShrink: 0, color: 'var(--text-muted)', fontSize: 11, fontVariantNumeric: 'tabular-nums' }}>{Number(s.ms || 0).toFixed(1)}ms</span>
                    <div style={{ position: 'relative', flex: 1, height: 20 }}>
                      {/* 时间轴背景 */}
                      <div style={{ position: 'absolute', inset: '6px 0 0 0', background: 'repeating-linear-gradient(90deg, transparent, transparent 19px, var(--border-soft) 19px, var(--border-soft) 20px)', opacity: .6 }} />
                      {/* 耗时条：宽度 ∝ 耗时 / 总耗时 */}
                      <div style={{
                        position: 'absolute', top: 1, left: `${(spanStartBase && s.start_time ? ((Number(s.start_time) - spanStartBase)) / (maxMs * 1e6 + 1) : 0)}%`,
                        width: `${Math.max(2, (Number(s.ms || 0) / maxMs) * 100)}%`, height: 18,
                        background: s.is_error ? 'var(--danger)' : 'var(--primary)', borderRadius: 4, opacity: .82, minWidth: 4,
                      }} title={`${s.operation_name} ${Number(s.ms || 0).toFixed(1)}ms`} />
                    </div>
                    <span style={{ width: depth * 16, flexShrink: 0 }} />
                    <span style={{ fontSize: 12, fontFamily: 'var(--font-mono)', marginLeft: 6, flexShrink: 0 }}>{s.service_name}</span>
                    <span style={{ marginLeft: 8, color: 'var(--text-muted)', fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.operation_name}</span>
                    {s.is_error ? <StatusBadge text="err" tone="crit" /> : null}
                  </div>
                ))}
              </div>
            )}
            {detail?.root?.error_message && (
              <div style={{ marginTop: 12, padding: 10, background: 'var(--danger-soft)', borderRadius: 8, color: 'var(--danger)', fontSize: 12 }}>{detail.root.error_message}</div>
            )}
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default Trace
