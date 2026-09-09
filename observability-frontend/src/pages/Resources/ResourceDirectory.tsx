import React, { useEffect, useMemo, useState } from 'react'
import { Button, Input, Select, Space, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { getResourceCatalog, toResourceApiError, type ResourceCatalogItem } from '../../api/resources'
import DataState from '../../components/display/DataState'
import type { PlatformResourceRef, ResourceDomain, ResourceHealth } from '../../features/resources/types'
import { resourceTypeLabel } from '../../features/resources/resourceDomain'

const DOMAINS: Array<{ value: ResourceDomain; label: string }> = [
  { value: 'compute', label: '计算' }, { value: 'network', label: '网络' }, { value: 'storage', label: '存储' }, { value: 'kubernetes', label: 'Kubernetes' }, { value: 'application', label: '应用服务' },
]
const HEALTH_OPTIONS = [{ value: '', label: '全部健康状态' }, { value: 'critical', label: '严重' }, { value: 'degraded', label: '降级' }, { value: 'risk', label: '风险' }, { value: 'healthy', label: '正常' }, { value: 'unknown', label: '未知' }]

export interface ResourceDirectoryProps {
  clusterId: string
  domain?: ResourceDomain
  selected?: PlatformResourceRef
  onSelect?: (resource: PlatformResourceRef) => void
}

export function ResourceDirectory({ clusterId, domain, selected, onSelect }: ResourceDirectoryProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const urlDomain = (searchParams.get('domain') || domain || '') as ResourceDomain | ''
  const [query, setQuery] = useState(searchParams.get('q') || '')
  const [health, setHealth] = useState<ResourceHealth | ''>((searchParams.get('health') || '') as ResourceHealth | '')
  const [items, setItems] = useState<ResourceCatalogItem[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [errorKind, setErrorKind] = useState<'forbidden' | 'error' | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setErrorKind(null)
    void getResourceCatalog({ domain: urlDomain || undefined, q: query.trim() || undefined, health: health || undefined, limit: 50 }, controller.signal)
      .then((response) => { setItems(response.items.filter((item) => item.clusterId === clusterId)); setCursor(response.nextCursor) })
      .catch((error) => { if (!controller.signal.aborted) setErrorKind(toResourceApiError(error).kind === 'forbidden' ? 'forbidden' : 'error') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [clusterId, health, query, urlDomain])

  const title = useMemo(() => DOMAINS.find((item) => item.value === urlDomain)?.label || '全部资源', [urlDomain])

  function updateFilter(key: string, value: string): void {
    setSearchParams((current) => {
      if (value) current.set(key, value)
      else current.delete(key)
      return current
    }, { replace: true })
  }

  function toRef(item: ResourceCatalogItem): PlatformResourceRef {
    return { clusterId: item.clusterId, uid: item.uid, type: item.type, domain: item.domain, name: item.name, ...(item.namespace ? { namespace: item.namespace } : {}) }
  }

  async function loadMore(): Promise<void> {
    if (!cursor) return
    const response = await getResourceCatalog({ domain: urlDomain || undefined, q: query.trim() || undefined, health: health || undefined, limit: 50, cursor })
    setItems((current) => [...current, ...response.items.filter((item) => item.clusterId === clusterId)])
    setCursor(response.nextCursor)
  }

  if (loading && items.length === 0) return <DataState kind="loading" />
  if (errorKind === 'forbidden') return <DataState kind="forbidden" />
  if (errorKind === 'error') return <DataState kind="error" title="资源目录读取失败" onRetry={() => setQuery((current) => `${current} `)} />
  return (
    <section className="resource-directory" aria-label="资源目录">
      <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>{title}</Typography.Title><Typography.Text type="secondary">当前集群资源目录，使用 UID 区分同名资源</Typography.Text></div><Tag>{items.length} 项</Tag></div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Input.Search aria-label="资源目录搜索" placeholder="搜索名称或 UID" value={query} onChange={(event) => { setQuery(event.target.value); updateFilter('q', event.target.value) }} allowClear style={{ width: 250 }} />
        <Select aria-label="健康状态" value={health} onChange={(value) => { setHealth(value as ResourceHealth | ''); updateFilter('health', value) }} options={HEALTH_OPTIONS} style={{ width: 150 }} />
      </Space>
      <div className="resource-directory__list">
        {items.map((item) => <Button key={item.uid} type="text" className={`resource-directory__row${selected?.uid === item.uid ? ' is-selected' : ''}`} aria-label={`${resourceTypeLabel(item.type)} ${item.location || item.name}`} onClick={() => onSelect?.(toRef(item))}>
          <span className="resource-directory__row-main"><strong>{item.name}</strong><span>{resourceTypeLabel(item.type)} · {item.location || item.name}</span><small>{item.uid}</small></span><Tag color={item.health === 'critical' ? 'red' : item.health === 'degraded' || item.health === 'risk' ? 'orange' : item.health === 'healthy' ? 'green' : 'default'}>{item.health}</Tag>
        </Button>)}
        {items.length === 0 && <DataState kind="empty" compact title="当前域暂无资源" />}
      </div>
      {cursor && <Button block onClick={() => void loadMore()}>加载更多资源</Button>}
    </section>
  )
}

export default ResourceDirectory
