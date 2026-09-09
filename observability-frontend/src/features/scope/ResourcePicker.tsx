import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Input, Switch, Tag } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { getResourceCatalog, getResourceDetail, toResourceApiError, type ResourceCatalogItem } from '../../api/resources'
import type { PlatformResourceRef, ResourceDomain, ResourceHealth } from '../resources/types'
import { isSelectableResourceType, resourceDomainOf, resourceLocation, resourceTypeLabel } from '../resources/resourceDomain'

const RECENT_RESOURCES_KEY = 'aiops-recent-resources'
const DOMAIN_LABELS: Record<ResourceDomain, string> = {
  compute: '计算',
  network: '网络',
  storage: '存储',
  kubernetes: 'Kubernetes',
  application: '应用',
}
const ABNORMAL_HEALTH: ResourceHealth[] = ['critical', 'degraded', 'risk']

export interface ResourcePickerProps {
  clusterId: string
  value?: PlatformResourceRef
  disabled?: boolean
  onChange: (resource?: PlatformResourceRef) => void
}

function readRecentResources(clusterId: string): ResourceCatalogItem[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(RECENT_RESOURCES_KEY) || '[]') as ResourceCatalogItem[]
    return Array.isArray(parsed) ? parsed.filter((item) => item?.clusterId === clusterId && isSelectableResourceType(item.type)) : []
  } catch {
    return []
  }
}

function writeRecentResource(item: ResourceCatalogItem): void {
  try {
    const current = JSON.parse(window.localStorage.getItem(RECENT_RESOURCES_KEY) || '[]') as ResourceCatalogItem[]
    const next = [item, ...(Array.isArray(current) ? current : []).filter((entry) => entry.uid !== item.uid)].slice(0, 8)
    window.localStorage.setItem(RECENT_RESOURCES_KEY, JSON.stringify(next))
  } catch {
    // Recent resources are a convenience only; storage failures must not block selection.
  }
}

function toResourceRef(item: ResourceCatalogItem, clusterId: string): PlatformResourceRef | undefined {
  if (item.clusterId !== clusterId) return undefined
  const domain = resourceDomainOf(item.type)
  if (!domain) return undefined
  return {
    clusterId: item.clusterId,
    uid: item.uid,
    type: item.type,
    domain,
    name: item.name,
    ...(domain === 'kubernetes' && item.namespace ? { namespace: item.namespace } : {}),
  }
}

function resourceFromRef(resource: PlatformResourceRef): ResourceCatalogItem {
  return {
    clusterId: resource.clusterId,
    uid: resource.uid,
    type: resource.type,
    domain: resource.domain,
    name: resource.name,
    ...(resource.namespace ? { namespace: resource.namespace } : {}),
    location: resourceLocation(resource),
    health: 'unknown',
    source: 'scope',
  }
}

export function ResourcePicker({ clusterId, value, disabled = false, onChange }: ResourcePickerProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [onlyAbnormal, setOnlyAbnormal] = useState(false)
  const [items, setItems] = useState<ResourceCatalogItem[]>([])
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const optionRefs = useRef<Array<HTMLDivElement | null>>([])
  const resourceParam = searchParams.get('resource')

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), 250)
    return () => window.clearTimeout(timer)
  }, [query])

  useEffect(() => {
    if (!open || debouncedQuery.length < 2 || !clusterId) return
    const controller = new AbortController()
    setLoading(true)
    setMessage(null)
    void getResourceCatalog({ q: debouncedQuery, limit: 40, health: undefined }, controller.signal)
      .then((response) => setItems(response.items.filter((item) => item.clusterId === clusterId)))
      .catch((error) => {
        if (!controller.signal.aborted) setMessage(toResourceApiError(error).kind === 'forbidden' ? '当前角色无权读取资源目录' : '资源目录暂时不可用')
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [clusterId, debouncedQuery, open])

  useEffect(() => {
    if (!clusterId || !resourceParam) return
    const controller = new AbortController()
    setMessage(null)
    void getResourceDetail(resourceParam, controller.signal)
      .then((response) => {
        const resource = toResourceRef(response.data, clusterId)
        if (!resource) {
          setSearchParams((current) => { current.delete('resource'); return current }, { replace: true })
          setMessage('资源不属于当前集群，已清除资源范围')
          return
        }
        onChange(resource)
        writeRecentResource(response.data)
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        const apiError = toResourceApiError(error)
        setSearchParams((current) => { current.delete('resource'); return current }, { replace: true })
        setMessage(apiError.kind === 'forbidden' ? '当前角色无权访问该资源，已清除资源范围' : '资源不存在或已不可用，已清除资源范围')
      })
    return () => controller.abort()
    // URL restoration is intentionally keyed by the URL value, not by the selected value.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, resourceParam])

  const recentItems = useMemo(() => readRecentResources(clusterId), [clusterId, open, value?.uid])
  const visibleItems = useMemo(() => {
    const source = debouncedQuery.length >= 2 ? items : recentItems
    const filtered = onlyAbnormal ? source.filter((item) => ABNORMAL_HEALTH.includes(item.health)) : source
    const deduped = new Map(filtered.map((item) => [item.uid, item]))
    if (value && !deduped.has(value.uid)) deduped.set(value.uid, resourceFromRef(value))
    return Array.from(deduped.values())
  }, [debouncedQuery, items, onlyAbnormal, recentItems, value])

  const groupedItems = useMemo(() => {
    const groups = new Map<ResourceDomain, ResourceCatalogItem[]>()
    visibleItems.forEach((item) => {
      const domain = resourceDomainOf(item.type)
      if (!domain) return
      groups.set(domain, [...(groups.get(domain) ?? []), item])
    })
    return Array.from(groups.entries())
  }, [visibleItems])

  function updateUrl(resource?: PlatformResourceRef): void {
    setSearchParams((current) => {
      if (resource) current.set('resource', resource.uid)
      else current.delete('resource')
      return current
    }, { replace: true })
  }

  function selectItem(item: ResourceCatalogItem): void {
    const resource = toResourceRef(item, clusterId)
    if (!resource) return
    onChange(resource)
    writeRecentResource(item)
    updateUrl(resource)
    setOpen(false)
    setMessage(null)
  }

  function clearSelection(event?: React.MouseEvent): void {
    event?.stopPropagation()
    onChange(undefined)
    updateUrl(undefined)
    setMessage(null)
  }

  return (
    <div className="resource-picker" aria-busy={loading}>
      <div className="resource-picker__control">
        <button
          type="button"
          role="combobox"
          aria-label="资源"
          aria-expanded={open}
          aria-controls="resource-picker-options"
          disabled={disabled}
          className="resource-picker__trigger"
          onClick={() => setOpen((current) => !current)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
              event.preventDefault()
              setOpen(true)
            }
          }}
        >
          {value ? `${resourceTypeLabel(value.type)} · ${resourceLocation(value)}` : '选择平台资源'}
        </button>
        {value && <button type="button" className="resource-picker__clear" aria-label="清除资源" disabled={disabled} onClick={clearSelection}>×</button>}
      </div>
      {message && <div className="resource-picker__message" role="alert">{message}</div>}
      {open && !disabled && (
        <div className="resource-picker__popover">
          <div className="resource-picker__filters">
            <Input
              autoFocus
              allowClear
              aria-label="搜索资源"
              placeholder="搜索资源名称、UID（至少 2 个字符）"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Escape') setOpen(false)
                if (event.key === 'ArrowDown') optionRefs.current[0]?.focus()
              }}
            />
            <label className="resource-picker__abnormal"><Switch size="small" checked={onlyAbnormal} onChange={setOnlyAbnormal} /> 仅异常资源</label>
          </div>
          {loading && <div className="resource-picker__state">正在检索资源…</div>}
          {!loading && debouncedQuery.length < 2 && recentItems.length === 0 && <div className="resource-picker__state">输入至少 2 个字符开始检索，或从最近访问中选择</div>}
          {!loading && debouncedQuery.length >= 2 && visibleItems.length === 0 && <div className="resource-picker__state">未找到匹配资源</div>}
          {!loading && visibleItems.length > 0 && (
            <div id="resource-picker-options" role="listbox" aria-label="资源候选项" className="resource-picker__options">
              {groupedItems.map(([domain, domainItems]) => (
                <section key={domain} className="resource-picker__group">
                  <div className="resource-picker__group-title">{DOMAIN_LABELS[domain]} <span>{domainItems.length}</span></div>
                  {domainItems.map((item, index) => {
                    const selected = value?.uid === item.uid
                    return (
                      <div
                        key={item.uid}
                        ref={(element) => { optionRefs.current[index] = element }}
                        role="option"
                        aria-selected={selected}
                        tabIndex={0}
                        className={`resource-picker__option${selected ? ' is-selected' : ''}`}
                        onClick={() => selectItem(item)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectItem(item) }
                        }}
                      >
                        <span className="resource-picker__option-main"><strong>{resourceTypeLabel(item.type)}</strong><span>{item.location || item.name}</span></span>
                        <Tag color={item.health === 'critical' ? 'red' : item.health === 'degraded' || item.health === 'risk' ? 'orange' : item.health === 'healthy' ? 'green' : 'default'}>{item.health}</Tag>
                      </div>
                    )
                  })}
                </section>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

export default ResourcePicker
