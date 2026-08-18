import React, { useEffect, useMemo, useState } from 'react'
import { Table, Tag, Spin, Space } from 'antd'
import { listNodeHealth, listIpmiSensors, listIpmiEvents } from '../../api/client'
import { PageHeader, Breadcrumb, Empty, StatusBadge, StatusTone } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

// 状态 → 展示（兼容 healthy/degraded/fault、ok/warning/critical、normal/warn/error 等多套取值）
function healthTone(s?: string): { tone: StatusTone; label: string } {
  const v = String(s || '').toLowerCase()
  if (['healthy', 'ok', 'normal', 'good', 'up', 'pass'].includes(v)) return { tone: 'ok', label: '正常' }
  if (['degraded', 'warning', 'warn', 'low', 'caution', 'minor'].includes(v)) return { tone: 'warn', label: '亚健康' }
  if (['fault', 'critical', 'crit', 'bad', 'down', 'error', 'failed', 'alarm'].includes(v)) return { tone: 'crit', label: '故障' }
  return { tone: 'muted', label: s || '未知' }
}

function fmtTime(v?: string): string {
  if (!v) return '-'
  return String(v).replace('T', ' ').slice(0, 19)
}

// 部件中文名映射（列渲染兜底）
const COMPONENT_LABEL: Record<string, string> = {
  cpu: 'CPU', memory: '内存', mem: '内存', disk: '磁盘', network: '网络', net: '网络',
  temperature: '温度', temp: '温度', power: '电源', fan: '风扇', voltage: '电压',
}

// 总体状态 = 各部件最差档位
function worstStatus(parts: (string | undefined)[]): string {
  const order = ['fault', 'critical', 'crit', 'bad', 'error', 'degraded', 'warning', 'warn', 'healthy', 'ok', 'normal']
  let worst = ''
  for (const p of parts) {
    if (!p) continue
    const v = String(p).toLowerCase()
    const a = order.indexOf(v)
    const b = order.indexOf(worst)
    if (a >= 0 && (b < 0 || a < b)) worst = v
  }
  return worst
}

const Hardware: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)

  // ① 节点硬件健康（部件可用性，按 node 透视成一行）
  const [health, setHealth] = useState<any[]>([])
  const [healthLoading, setHealthLoading] = useState(true)
  // ② IPMI 传感器（温度/风扇/电源/电压分组）
  const [sensors, setSensors] = useState<any[]>([])
  const [sensorLoading, setSensorLoading] = useState(true)
  // ③ SEL 事件流
  const [events, setEvents] = useState<any[]>([])
  const [eventLoading, setEventLoading] = useState(true)

  useEffect(() => {
    let alive = true
    setHealthLoading(true); setSensorLoading(true); setEventLoading(true)
    listNodeHealth()
      .then((r) => {
        const d = r.data
        const list = Array.isArray(d) ? d : d?.health ?? d?.data ?? d?.nodes ?? []
        if (alive) setHealth(Array.isArray(list) ? list : [])
      })
      .catch(() => { if (alive) setHealth([]) })
      .finally(() => { if (alive) setHealthLoading(false) })

    listIpmiSensors()
      .then((r) => {
        const d = r.data
        const list = Array.isArray(d) ? d : d?.sensors ?? d?.data ?? []
        if (alive) setSensors(Array.isArray(list) ? list : [])
      })
      .catch(() => { if (alive) setSensors([]) })
      .finally(() => { if (alive) setSensorLoading(false) })

    listIpmiEvents()
      .then((r) => {
        const d = r.data
        const list = Array.isArray(d) ? d : d?.events ?? d?.data ?? []
        if (alive) setEvents(Array.isArray(list) ? list : [])
      })
      .catch(() => { if (alive) setEvents([]) })
      .finally(() => { if (alive) setEventLoading(false) })

    return () => { alive = false }
  }, [currentClusterId])

  // 节点健康透视：component 行 → 每节点一行（cpu/memory/disk/network/temperature/power 列）
  const healthRows = useMemo(() => {
    if (!health.length) return []
    const isFlat = health.some((r: any) => (r?.node || r?.cpu) && !r?.component)
    if (isFlat) return health
    const byNode = new Map<string, any>()
    for (const r of health) {
      const name = r?.node_name ?? r?.node ?? 'unknown'
      if (!byNode.has(name)) byNode.set(name, { key: name, node: name, updated_at: r?.updated_at })
      const row = byNode.get(name)!
      const comp = String(r?.component ?? '').toLowerCase()
      if (comp) row[comp] = r?.status ?? ''
    }
    return Array.from(byNode.values())
  }, [health])

  const renderHealth = (v?: string) => {
    if (!v) return <span style={{ color: 'var(--text-muted)' }}>-</span>
    const h = healthTone(v)
    return <StatusBadge text={h.label} tone={h.tone} />
  }

  const healthCols = [
    { title: '节点', dataIndex: 'node', key: 'node', render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span> },
    { title: 'CPU', dataIndex: 'cpu', key: 'cpu', render: renderHealth },
    { title: '内存', dataIndex: 'memory', key: 'memory', render: (v: string, r: any) => renderHealth(v ?? r?.mem) },
    { title: '磁盘', dataIndex: 'disk', key: 'disk', render: renderHealth },
    { title: '网络', dataIndex: 'network', key: 'network', render: renderHealth },
    { title: '温度', dataIndex: 'temperature', key: 'temperature', render: (v: string, r: any) => renderHealth(v ?? r?.temp) },
    { title: '电源', dataIndex: 'power', key: 'power', render: renderHealth },
    {
      title: '总体状态', key: 'overall',
      render: (_: unknown, r: any) => {
        const w = worstStatus([r?.cpu, r?.memory ?? r?.mem, r?.disk, r?.network, r?.temperature ?? r?.temp, r?.power])
        return w ? <StatusBadge text={healthTone(w).label} tone={healthTone(w).tone} /> : <span style={{ color: 'var(--text-muted)' }}>-</span>
      },
    },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', width: 170, render: fmtTime },
  ]

  // IPMI 传感器分组：温度 / 风扇 / 电源 / 电压（其余归"其他"）
  const sensorGroups = useMemo(() => {
    const groups: { key: string; label: string; items: any[] }[] = [
      { key: 'temperature', label: '温度', items: [] },
      { key: 'fan', label: '风扇', items: [] },
      { key: 'power', label: '电源', items: [] },
      { key: 'voltage', label: '电压', items: [] },
      { key: 'other', label: '其他', items: [] },
    ]
    const bucket: Record<string, any[]> = {}
    for (const g of groups) bucket[g.key] = g.items
    for (const s of sensors) {
      const t = String(s?.sensor_type ?? '').toLowerCase()
      const key = t.includes('temp') ? 'temperature'
        : t.includes('fan') ? 'fan'
          : (t.includes('power') || t.includes('psu')) ? 'power'
            : t.includes('volt') ? 'voltage'
              : 'other'
      bucket[key].push(s)
    }
    return groups
  }, [sensors])

  const sensorCols = [
    { title: '传感器', dataIndex: 'sensor_name', key: 'sensor_name', render: (v: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
    { title: '节点', dataIndex: 'node_name', key: 'node_name', width: 150, render: (v: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
    { title: '读数', dataIndex: 'reading', key: 'reading', width: 130, render: (v: string) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{v || '-'}</span> },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => {
        const h = healthTone(v)
        return <Tag color={h.tone === 'ok' ? 'green' : h.tone === 'warn' ? 'orange' : h.tone === 'crit' ? 'red' : 'default'} style={{ marginRight: 0 }}>{h.label}</Tag>
      },
    },
  ]

  // SEL 事件流列：时间/节点/类型/严重度/消息
  const eventCols = [
    { title: '时间', dataIndex: 'event_time', key: 'event_time', width: 170, render: (v: string, r: any) => <span style={{ fontSize: 12 }}>{fmtTime(v ?? r?.time ?? r?.created_at)}</span> },
    { title: '节点', dataIndex: 'node_name', key: 'node_name', width: 160, render: (v: string, r: any) => <span style={{ fontSize: 12 }}>{v ?? r?.node ?? '-'}</span> },
    { title: '类型', dataIndex: 'sensor', key: 'sensor', width: 140, render: (v: string, r: any) => <span style={{ fontSize: 12 }}>{v ?? r?.event_type ?? r?.type ?? '-'}</span> },
    {
      title: '严重度', dataIndex: 'severity', key: 'severity', width: 110,
      render: (v: string) => {
        const h = healthTone(v)
        return <Tag color={h.tone === 'ok' ? 'green' : h.tone === 'warn' ? 'orange' : h.tone === 'crit' ? 'red' : 'default'} style={{ marginRight: 0 }}>{h.label}</Tag>
      },
    },
    { title: '消息', dataIndex: 'event_desc', key: 'event_desc', render: (v: string, r: any) => <span style={{ fontSize: 12 }}>{v ?? r?.message ?? '-'}</span> },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '基础设施' }, { t: '硬件健康' }]} />
      <PageHeader title="硬件健康" desc="节点部件可用性 · IPMI 传感器（温度/风扇/电源/电压）· SEL 系统事件流" />

      {/* ① 节点硬件健康表 */}
      <div className="card" style={{ padding: 0, marginBottom: 16 }}>
        <div className="card__head" style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)' }}>
          <div className="card__title">节点硬件健康</div>
        </div>
        <Table rowKey={(r: any) => `${r?.node ?? r?.node_name ?? ''}-${r?.component || 'overall'}`} loading={healthLoading} columns={healthCols} dataSource={healthRows}
          size="middle" pagination={{ pageSize: 10, showSizeChanger: false }} scroll={{ x: 980 }}
          locale={{ emptyText: <Empty text="暂无节点健康数据" /> }} />
      </div>

      {/* ② IPMI 传感器 */}
      <div className="card" style={{ padding: 0, marginBottom: 16 }}>
        <div className="card__head" style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)' }}>
          <div className="card__title">IPMI 传感器</div>
        </div>
        <Spin spinning={sensorLoading}>
          {sensors.length === 0 ? (
            <Empty text="暂无 IPMI 传感器数据" hint="待 ipmi-exporter 上报后展示温度/风扇/电源/电压" />
          ) : (
            <div style={{ padding: 16 }}>
              {sensorGroups.map((g) => (
                <div key={g.key} style={{ marginBottom: g === sensorGroups[sensorGroups.length - 1] ? 0 : 16 }}>
                  <div style={{ marginBottom: 8 }}>
                    <Tag color="blue">{g.label}</Tag>
                    <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{g.items.length} 项</span>
                  </div>
                  {g.items.length > 0 ? (
                    <Table rowKey={(r: any) => `${r?.id ?? ''}-${r?.node_name}-${r?.sensor_name}-${r?.reading}`} columns={sensorCols} dataSource={g.items}
                      size="small" pagination={false} scroll={{ x: 640 }} />
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '4px 0' }}>暂无{g.label}传感器</div>
                  )}
                </div>
              ))}
            </div>
          )}
        </Spin>
      </div>

      {/* ③ SEL 事件流 */}
      <div className="card" style={{ padding: 0 }}>
        <div className="card__head" style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)' }}>
          <div className="card__title">SEL 事件流</div>
          <Space size={4}>
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>共 {events.length} 条</span>
          </Space>
        </div>
        <Table rowKey={(r: any) => `${r?.id ?? ''}-${r?.event_id ?? ''}-${r?.event_time ?? ''}-${r?.event_desc ?? ''}`}
          loading={eventLoading} columns={eventCols} dataSource={events}
          size="middle" pagination={{ pageSize: 15, showSizeChanger: false }} scroll={{ x: 860 }}
          locale={{ emptyText: <Empty text="暂无 SEL 事件" /> }} />
      </div>
    </div>
  )
}

export default Hardware
