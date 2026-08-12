import React, { useEffect, useState } from 'react'
import { Table, Segmented, Button, Space, Drawer, Spin } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { getAlertEvents, rcaAlertAnalysis } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

interface AlertEvent { id: string | number; severity?: string; labels?: any; summary?: string; description?: string; service_name?: string; startsAt?: string; status?: string }

const sevTone = (s: string): 'crit' | 'warn' | 'info' => (s === 'critical' || s === '严重' ? 'crit' : s === 'warning' || s === '警告' ? 'warn' : 'info')

const AlertEvents: React.FC = () => {
  const [searchParams] = useSearchParams()
  const [sev, setSev] = useState<string>('all')
  const [status, setStatus] = useState<string>('all') // 2.10 当前/历史告警
  const [data, setData] = useState<AlertEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<AlertEvent | null>(null)
  const [rca, setRca] = useState('')
  const [rcaLoading, setRcaLoading] = useState(false)

  // P1: 从规则页"历史告警"跳转携带 ?rule=，按 rule_id 过滤
  const ruleFilter = searchParams.get('rule') || ''

  const load = () => {
    setLoading(true)
    const params: Record<string, unknown> = { limit: 200 }
    if (ruleFilter) params.rule = ruleFilter
    getAlertEvents(params).then((r) => {
      const d = Array.isArray(r.data) ? r.data : r.data?.events || r.data?.data || []
      setData(d)
    }).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [ruleFilter])

  const severity = (e: AlertEvent) => e.severity || e.labels?.severity || e.labels?.level || 'warning'
  const eventStatus = (e: any) => e.status || e.state || (e.resolved_at ? 'resolved' : 'firing')
  // 2.10 当前告警=未解决（firing/acknowledged/空）；历史告警=全部
  const filtered = data
    .filter((e) => (sev === 'all' ? true : severity(e).toLowerCase().includes(sev)))
    .filter((e) => {
      if (status === 'all') return true
      const st = String(eventStatus(e)).toLowerCase()
      if (status === 'current') return st === 'firing' || st === 'acknowledged' || st === ''
      if (status === 'resolved') return st === 'resolved'
      return true
    })

  const cols = [
    { title: '严重度', key: 'severity', width: 90, render: (_: any, r: AlertEvent) => { const s = severity(r); return <StatusBadge text={s === 'critical' || s === '严重' ? '严重' : s === 'warning' || s === '警告' ? '警告' : '信息'} tone={sevTone(s)} /> } },
    { title: '摘要', key: 'summary', render: (_: any, r: any) => <a onClick={() => setDetail(r)} style={{ color: 'var(--text)' }}>{r.rule_name || r.message || r.summary || r.labels?.alertname || '告警'}</a> },
    { title: '告警对象', key: 'object', render: (_: any, r: any) => r.object || r.labels?.pod || r.labels?.deployment || r.labels?.service || r.service || '-' },
    { title: '次数', key: 'count', width: 80, render: (_: any, r: any) => <span style={{ color: 'var(--text-muted)' }}>{r.count ?? 1}</span> },
    { title: '触发时间', key: 'time', render: (_: any, r: any) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{r.last_timestamp || r.first_timestamp || '-'}</span> },
    { title: '操作', key: 'op', width: 110, render: (_: any, r: any) => (
        <Space size={4}>
          <Button size="small" type="link" onClick={() => { setDetail(r); setRca(''); runRca(r) }}>根因分析</Button>
        </Space>
      ) },
  ]

  // P3-1 美化：把 orchestrator 返回的 JSON RCA 结果解析为可读文本（根因 / 依据 / 处置方案）
  const renderRca = (raw: string) => {
    if (!raw) return ''
    let obj: any = null
    try { obj = JSON.parse(raw) } catch { /* 非 JSON，原文 */ }
    // 兼容 {"result":{...}} 包裹
    const data = obj?.result || obj
    if (data && typeof data === 'object') {
      const lines: string[] = []
      const pick = (keys: string[]) => { for (const k of keys) { if (data[k] !== undefined && data[k] !== null && data[k] !== '') return data[k] } return null }
      const rootCause = pick(['root_cause', 'likely_root_cause', 'cause', 'root'])
      const reason = pick(['reason', 'explanation', 'summary', 'analysis', 'conclusion'])
      const action = pick(['suggestion', 'action', 'suggested_action', 'recommendation', 'fix', 'remediation', '处置方案', '处置建议'])
      const impact = pick(['impact', 'affected', 'scope'])
      const sections: string[] = []
      if (typeof rootCause === 'string') sections.push('【可能根因】\n' + rootCause)
      else if (rootCause) sections.push('【可能根因】\n' + JSON.stringify(rootCause))
      if (typeof reason === 'string') sections.push('\n【分析依据】\n' + reason)
      else if (reason) sections.push('\n【分析依据】\n' + JSON.stringify(reason))
      if (typeof action === 'string') sections.push('\n【处置方案】\n' + action)
      else if (action) sections.push('\n【处置方案】\n' + JSON.stringify(action))
      if (typeof impact === 'string') sections.push('\n【影响范围】\n' + impact)
      // 兜底：若有未解析的其余字段，补一行
      if (sections.length === 0) return JSON.stringify(data, null, 2)
      return sections.join('\n')
    }
    return raw
  }

  const runRca = (r: any) => {
    setRcaLoading(true)
    setRca('')
    rcaAlertAnalysis({ summary: r.rule_name || r.message || '', service: r.service || '', severity: severity(r) })
      .then((res) => setRca(typeof res.data === 'string' ? res.data : JSON.stringify(res.data)))
      .catch((e) => setRca(`RCA 分析失败：${e?.response?.data?.error || e.message}`))
      .finally(() => setRcaLoading(false))
  }

  return (
    <div>
      <Breadcrumb items={[{ t: '告警' }, { t: '告警事件' }]} />
      <PageHeader title="告警事件" desc="分级处理告警 · 当前 / 历史 · AI 根因分析与处置方案"
        actions={<Space wrap>
          <Segmented value={status} onChange={(v) => setStatus(v as string)} options={[{ label: '当前告警', value: 'current' }, { label: '历史告警', value: 'resolved' }, { label: '全部', value: 'all' }]} />
          <Segmented value={sev} onChange={(v) => setSev(v as string)} options={[{ label: '全部', value: 'all' }, { label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }, { label: '信息', value: 'info' }]} />
        </Space>} />

      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={filtered} size="middle"
          pagination={{ pageSize: 20 }} scroll={{ x: 900 }} locale={{ emptyText: <Empty text="暂无告警" /> }} />
      </div>

      <Drawer width={560} open={!!detail} onClose={() => setDetail(null)} title={(detail as any)?.rule_name || detail?.summary || (detail as any)?.labels?.alertname || '告警详情'}
        styles={{ body: { padding: 16 } }}>
        {detail && (
          <div>
            <div className="card" style={{ padding: 0, marginBottom: 12 }}>
              <Table rowKey="k" size="small" pagination={false} showHeader={false}
                dataSource={[
                  { k: '规则', v: (detail as any).rule_name || detail.summary || '-' },
                  { k: '服务', v: (detail as any).service || (detail as any).labels?.service || '-' },
                  { k: '严重度', v: severity(detail) },
                  { k: '消息', v: (detail as any).message || detail.description || '-' },
                  { k: '触发次数', v: `${(detail as any).count ?? 1}` },
                  { k: '首次触发', v: (detail as any).first_timestamp || '-' },
                  { k: '最近触发', v: (detail as any).last_timestamp || '-' },
                ]}
                columns={[
                  { title: '', dataIndex: 'k', key: 'k', width: 90, render: (v: string) => <span style={{ color: 'var(--text-muted)', fontWeight: 600 }}>{v}</span> },
                  { title: '', dataIndex: 'v', key: 'v', render: (v: string) => <span style={{ whiteSpace: 'pre-wrap' }}>{v}</span> },
                ]}
              />
            </div>
            <div style={{ marginTop: 12, fontWeight: 600, marginBottom: 8 }}>AI 根因分析</div>
            {rcaLoading ? <Spin /> : <div style={{ whiteSpace: 'pre-wrap', fontSize: 13, lineHeight: 1.7 }}>{renderRca(rca) || '点击下方按钮触发根因分析'}</div>}
            {!rcaLoading && <Button size="small" type="primary" ghost style={{ marginTop: 8 }} onClick={() => runRca(detail)}>{rca ? '重新分析' : '开始根因分析'}</Button>}
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default AlertEvents
