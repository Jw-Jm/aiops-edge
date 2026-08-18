import React, { useEffect, useRef, useState } from 'react'
import { Button, Input, Spin, Tag, Tooltip } from 'antd'
import {
  getGrafanaHealth, searchGrafanaDashboards,
  type GrafanaHealth, type GrafanaDashboardItem,
} from '../../api/grafana'
import { PageHeader, Breadcrumb, Empty, StatusBadge } from '../../components/ui/PageKit'

const IFRAME_BASE = '/grafana/d/'

const Grafana: React.FC = () => {
  const [health, setHealth] = useState<GrafanaHealth | null>(null)
  const [healthOk, setHealthOk] = useState<boolean | null>(null)
  const [q, setQ] = useState('')
  const [dashboards, setDashboards] = useState<GrafanaDashboardItem[]>([])
  const [loading, setLoading] = useState(false)
  const [searched, setSearched] = useState(false)
  const [err, setErr] = useState('')
  const [viewing, setViewing] = useState<GrafanaDashboardItem | null>(null)
  const [iframeLoading, setIframeLoading] = useState(false)
  const [iframeError, setIframeError] = useState(false)
  const [reloadTick, setReloadTick] = useState(0)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const iframeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // 健康检查：30s 轮询，Grafana 后启动可自动恢复；卸载时清理
  useEffect(() => {
    const checkHealth = () => {
      getGrafanaHealth()
        .then((r) => { setHealth(r.data); setHealthOk(true) })
        .catch(() => { setHealthOk(false); setHealth(null) })
    }
    checkHealth()
    const interval = setInterval(checkHealth, 30000)
    return () => clearInterval(interval)
  }, [])

  // 搜索：防抖 400ms
  useEffect(() => {
    if (timer.current) clearTimeout(timer.current)
    const query = q.trim()
    if (!query) { setDashboards([]); setSearched(false); setErr(''); return }
    timer.current = setTimeout(() => {
      setLoading(true); setErr('')
      searchGrafanaDashboards(query)
        .then((r) => {
          const list = Array.isArray(r.data) ? r.data : (r.data as any)?.dashboards || []
          setDashboards(list)
          setSearched(true)
        })
        .catch((e) => {
          setErr(e?.response?.data?.error || e?.message || '搜索失败')
          setDashboards([]); setSearched(false)
        })
        .finally(() => setLoading(false))
    }, 400)
    return () => { if (timer.current) clearTimeout(timer.current) }
  }, [q])

  // iframe 加载状态：进入仪表盘时置 loading，onLoad/onError 清除；
  // 兜底超时（20s）判定加载失败（部分浏览器 iframe onError 不触发）
  useEffect(() => {
    if (!viewing) return
    setIframeLoading(true)
    setIframeError(false)
    if (iframeTimer.current) clearTimeout(iframeTimer.current)
    iframeTimer.current = setTimeout(() => {
      setIframeLoading(false)
      setIframeError(true)
    }, 20000)
    return () => { if (iframeTimer.current) clearTimeout(iframeTimer.current) }
  }, [viewing, reloadTick])

  const deepLink = (d: GrafanaDashboardItem) =>
    `${IFRAME_BASE}${d.uid}?theme=light`

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: 'Grafana' }]} />
      <PageHeader title="Grafana 集成" desc="搜索 DeepFlow 内置仪表盘 · iframe 嵌入浏览 · 支持新窗口打开深链"
        actions={healthOk === null
          ? <StatusBadge text="检测中…" tone="muted" />
          : healthOk
            ? <StatusBadge text={`Grafana 正常${health?.version ? ` · v${health.version}` : ''}`} tone="ok" />
            : <Tooltip title="query-api 的 /api/v1/grafana/health 代理不可达"><span><StatusBadge text="Grafana 不可达" tone="crit" /></span></Tooltip>} />

      <div className="card" style={{ padding: 16 }}>
        <Input.Search value={q} onChange={(e) => setQ(e.target.value)}
          placeholder="搜索仪表盘，如：DeepFlow / MySQL / Pod"
          loading={loading}
          allowClear style={{ maxWidth: 520 }} />
        {err && <div style={{ marginTop: 10, color: 'var(--danger)', fontSize: 12 }}>⚠ {err}</div>}
      </div>

      {viewing ? (
        <div className="card" style={{ padding: 0 }}>
          <div className="card__head">
            <div className="card__title">{viewing.title || viewing.uid}</div>
            <div style={{ display: 'flex', gap: 8 }}>
              <Button size="small" onClick={() => setViewing(null)}>← 返回列表</Button>
              <Button size="small" type="primary"
                onClick={() => window.open(deepLink(viewing), '_blank', 'noopener')}>
                在新窗口打开
              </Button>
            </div>
          </div>
          <div style={{ position: 'relative', height: 'calc(100vh - 300px)', minHeight: 420 }}>
            {iframeLoading && (
              <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(255,255,255,0.65)', zIndex: 2 }}>
                <Spin tip="仪表盘加载中…" />
              </div>
            )}
            {iframeError && (
              <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, background: 'var(--bg)', zIndex: 2 }}>
                <Empty text="仪表盘加载失败" hint="Grafana 可能未就绪，可稍后重试" />
                <Button size="small" onClick={() => setReloadTick((t) => t + 1)}>重试</Button>
              </div>
            )}
            {/* sandbox 取舍说明：iframe src 为同源代理（/grafana/d/...），Grafana 需同源加载其静态资源，
                故保留 allow-same-origin（最宽松组合，会削弱隔离）。若后续 Grafana 迁移到独立域名，
                应收紧为 sandbox="allow-scripts" 并配合 CSP frame-ancestors 隔离。 */}
            <iframe
              key={`${viewing.uid}-${reloadTick}`}
              title={`grafana-${viewing.uid}`}
              src={`${IFRAME_BASE}${viewing.uid}?theme=light&kiosk`}
              sandbox="allow-scripts allow-same-origin allow-forms"
              onLoad={() => { if (iframeTimer.current) clearTimeout(iframeTimer.current); setIframeLoading(false); setIframeError(false) }}
              onError={() => { if (iframeTimer.current) clearTimeout(iframeTimer.current); setIframeError(true); setIframeLoading(false) }}
              style={{ width: '100%', height: '100%', border: 'none' }} />
          </div>
          <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-soft)', fontSize: 12, color: 'var(--text-muted)' }}>
            深链地址：<span style={{ fontFamily: 'var(--font-mono)' }}>{deepLink(viewing)}</span>
          </div>
        </div>
      ) : loading ? (
        <div style={{ textAlign: 'center', padding: 60 }}><Spin tip="搜索中…" /></div>
      ) : !searched ? (
        <div className="card"><Empty text="输入关键词搜索仪表盘" hint="例如：DeepFlow 或 MySQL 等内置面板" /></div>
      ) : dashboards.length === 0 ? (
        <div className="card"><Empty text="未找到匹配的仪表盘" hint="换个关键词试试" /></div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
          {dashboards.map((d, i) => (
            <div key={d.uid || i} className="card" role="button" tabIndex={0}
              aria-label={`浏览仪表盘 ${d.title || d.uid}`}
              style={{ marginBottom: 0, cursor: 'pointer' }}
              onClick={() => setViewing(d)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  setViewing(d)
                }
              }}>
              <div className="card__body">
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                  <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--text)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {d.title || d.uid}
                  </div>
                  {d.type && <Tag style={{ flexShrink: 0 }}>{d.type}</Tag>}
                </div>
                {d.uid && (
                  <div style={{ marginTop: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
                    {d.uid}
                  </div>
                )}
                {Array.isArray(d.tags) && d.tags.length > 0 && (
                  <div style={{ marginTop: 10, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                    {d.tags.slice(0, 6).map((t) => <Tag key={t} style={{ marginRight: 0, fontSize: 11 }}>{t}</Tag>)}
                  </div>
                )}
                <div style={{ marginTop: 12, color: 'var(--primary)', fontSize: 12 }}>点击嵌入浏览 →</div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export default Grafana
