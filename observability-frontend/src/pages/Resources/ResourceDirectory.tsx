import React, { useEffect, useMemo, useState } from 'react'
import { Button, Input, Select, Space, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { getResourceCatalog, toResourceApiError, type ResourceCatalogItem, type ResourceCatalogParams } from '../../api/resources'
import DataState from '../../components/display/DataState'
import type { GraphEntityType } from '../../api/graphContracts'
import type { PlatformResourceRef, ResourceHealth } from '../../features/resources/types'
import { resourceTypeLabel } from '../../features/resources/resourceDomain'

const GROUPS = {
  containers: { label: '容器资源', hint: 'Deployment、StatefulSet、DaemonSet、Job、CronJob、Pod、Kubernetes Service、Ingress' },
  kubevirt: { label: 'KubeVirt 虚拟机', hint: 'VM 与 VMI；磁盘和 NAD 仅作为依赖关系展示' },
} as const
const HEALTH_OPTIONS = [{ value: '', label: '全部健康状态' }, { value: 'critical', label: '严重' }, { value: 'degraded', label: '降级' }, { value: 'risk', label: '风险' }, { value: 'healthy', label: '正常' }, { value: 'unknown', label: '未知' }]
const FRESHNESS_OPTIONS = [{ value: '', label: '全部时效' }, { value: 'fresh', label: '新鲜（≤5 分钟）' }, { value: 'stale', label: '陈旧（>5 分钟）' }, { value: 'unknown', label: '无观测时间' }]
const HEALTH_LABELS: Record<ResourceHealth, string> = { critical: '严重', degraded: '降级', risk: '风险', healthy: '正常', unknown: '未知' }
const TYPE_OPTIONS: Record<'containers' | 'kubevirt', Array<{ value: GraphEntityType; label: string }>> = {
  containers: [
    { value: 'deployment', label: 'Deployment' }, { value: 'statefulset', label: 'StatefulSet' },
    { value: 'daemonset', label: 'DaemonSet' }, { value: 'job', label: 'Job' }, { value: 'cronjob', label: 'CronJob' },
    { value: 'pod', label: 'Pod' }, { value: 'k8s_service', label: 'Service' }, { value: 'ingress', label: 'Ingress' },
  ],
  kubevirt: [{ value: 'vm', label: 'VM' }, { value: 'vmi', label: 'VMI' }],
}

export interface ResourceDirectoryProps {
  clusterId: string
  group?: 'containers' | 'kubevirt'
  selected?: PlatformResourceRef
  onSelect?: (resource: PlatformResourceRef) => void
}

export function ResourceDirectory({ clusterId, group = 'containers', selected, onSelect }: ResourceDirectoryProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedGroup = (searchParams.get('group') as 'containers' | 'kubevirt' | null) || group
  const activeGroup = selectedGroup in GROUPS ? selectedGroup : group
  const [query, setQuery] = useState(searchParams.get('q') || '')
  const [debouncedQuery, setDebouncedQuery] = useState(searchParams.get('q') || '')
  const [type, setType] = useState<GraphEntityType | ''>((searchParams.get('type') || '') as GraphEntityType | '')
  const [namespace, setNamespace] = useState(searchParams.get('namespace') || '')
  const [health, setHealth] = useState<ResourceHealth | ''>((searchParams.get('health') || '') as ResourceHealth | '')
  const [freshness, setFreshness] = useState<'' | 'fresh' | 'stale' | 'unknown'>((searchParams.get('freshness') || '') as '' | 'fresh' | 'stale' | 'unknown')
  const [items, setItems] = useState<ResourceCatalogItem[]>([])
  const [total, setTotal] = useState(0)
  const [cursor, setCursor] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [errorKind, setErrorKind] = useState<'forbidden' | 'error' | null>(null)

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query), 250)
    return () => window.clearTimeout(timer)
  }, [query])

  const params = useMemo<ResourceCatalogParams>(() => ({
    group: activeGroup,
    type: type || undefined,
    namespace: namespace.trim() || undefined,
    freshness: freshness || undefined,
    q: debouncedQuery.trim().length >= 2 ? debouncedQuery.trim() : undefined,
    health: health || undefined,
    limit: 50,
  }), [activeGroup, debouncedQuery, freshness, health, namespace, type])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setErrorKind(null)
    void getResourceCatalog(params, controller.signal)
      .then((response) => { setItems(response.items.filter((item) => item.clusterId === clusterId)); setTotal(response.total); setCursor(response.nextCursor) })
      .catch((error) => { if (!controller.signal.aborted) setErrorKind(toResourceApiError(error).kind === 'forbidden' ? 'forbidden' : 'error') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [clusterId, params])

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
    const response = await getResourceCatalog({ ...params, cursor })
    setItems((current) => [...current, ...response.items.filter((item) => item.clusterId === clusterId)])
    setCursor(response.nextCursor)
  }

  if (loading && items.length === 0) return <DataState kind="loading" />
  if (errorKind === 'forbidden') return <DataState kind="forbidden" />
  if (errorKind === 'error') return <DataState kind="error" title="资源目录读取失败" onRetry={() => setQuery((current) => `${current} `)} />
  const groupInfo = GROUPS[activeGroup]
  return (
    <section className="resource-directory" aria-label={`${groupInfo.label}目录`}>
      <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>{groupInfo.label}</Typography.Title><Typography.Text type="secondary">{groupInfo.hint}</Typography.Text></div><Tag>{items.length}/{total || items.length} 项</Tag></div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Input.Search aria-label="资源目录搜索" placeholder="搜索名称或 UID" value={query} onChange={(event) => { setQuery(event.target.value); updateFilter('q', event.target.value) }} allowClear style={{ width: 250 }} />
        <Select aria-label="资源类型" value={type} onChange={(value) => { setType(value as GraphEntityType | ''); updateFilter('type', value) }} options={[{ value: '', label: '全部资源类型' }, ...TYPE_OPTIONS[activeGroup]]} style={{ width: 160 }} />
        <Input aria-label="命名空间" placeholder="命名空间" value={namespace} onChange={(event) => { setNamespace(event.target.value); updateFilter('namespace', event.target.value) }} allowClear style={{ width: 150 }} />
        <Select aria-label="健康状态" value={health} onChange={(value) => { setHealth(value as ResourceHealth | ''); updateFilter('health', value) }} options={HEALTH_OPTIONS} style={{ width: 150 }} />
        <Select aria-label="数据时效" value={freshness} onChange={(value) => { setFreshness(value as typeof freshness); updateFilter('freshness', value) }} options={FRESHNESS_OPTIONS} style={{ width: 170 }} />
      </Space>
      <div className="resource-directory__list">
        {items.map((item) => <Button key={item.uid} type="text" className={`resource-directory__row${selected?.uid === item.uid ? ' is-selected' : ''}`} aria-label={`${resourceTypeLabel(item.type)} ${item.location || item.name}`} onClick={() => onSelect?.(toRef(item))}>
          <span className="resource-directory__row-main"><strong>{item.name}</strong><span>{resourceTypeLabel(item.type)} · {item.location || item.name}</span><small title={item.uid}>UID：{item.uid}</small><small>最近观测：{item.lastSeenAt ? item.lastSeenAt.slice(0, 19).replace('T', ' ') : '未提供'}</small></span><Tag color={item.health === 'critical' ? 'red' : item.health === 'degraded' || item.health === 'risk' ? 'orange' : item.health === 'healthy' ? 'green' : 'default'}>{HEALTH_LABELS[item.health]}</Tag>
        </Button>)}
        {items.length === 0 && <DataState kind="empty" compact title={`当前${groupInfo.label}暂无资源`} />}
      </div>
      {cursor && <Button block onClick={() => void loadMore()}>加载更多资源</Button>}
    </section>
  )
}

export default ResourceDirectory
