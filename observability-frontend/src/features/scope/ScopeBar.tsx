import React from 'react'
import { Select, Space, Tag } from 'antd'
import { useScopeStore } from '../../store/scopeStore'
import { formatTimeRange, type RunScopeSnapshot } from './types'

interface ScopeBarProps {
  snapshot?: RunScopeSnapshot
}

function scopeLabel(clusterId: string, clusters: Array<{ cluster_id: string; name: string }>): string {
  return clusters.find((cluster) => cluster.cluster_id === clusterId)?.name || clusterId || '未选择'
}

export function ScopeBar({ snapshot }: ScopeBarProps) {
  const context = useScopeStore((state) => state.context)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const clusters = useScopeStore((state) => state.clusters)
  const switchCluster = useScopeStore((state) => state.switchCluster)
  const setEnvironment = useScopeStore((state) => state.setEnvironment)
  const setNamespace = useScopeStore((state) => state.setNamespace)
  const setResource = useScopeStore((state) => state.setResource)
  const setTimeRange = useScopeStore((state) => state.setTimeRange)

  if (snapshot) {
    const snapshotParts = [snapshot.environment, scopeLabel(snapshot.clusterId, clusters), snapshot.namespace, snapshot.resource?.label, snapshot.timeRange ? formatTimeRange(snapshot.timeRange) : '时间范围未提供'].filter(Boolean)
    return (
      <div className="scope-bar scope-bar--snapshot" aria-label="调查快照">
        <Tag color="default">调查快照</Tag>
        <span>{snapshotParts.join(' / ')}</span>
      </div>
    )
  }

  const clusterOptions = clusters.map((cluster) => ({ value: cluster.cluster_id, label: cluster.name }))
  const timeValue = context.timeRange.mode === 'relative' ? String(context.timeRange.minutes) : 'absolute'
  return (
    <div className="scope-bar" aria-label="活动作用域">
      <Select aria-label="环境" size="small" value={context.environment} onChange={setEnvironment}
        options={[{ value: 'prod', label: 'prod' }, { value: 'staging', label: 'staging' }, { value: 'test', label: 'test' }, { value: 'dev', label: 'dev' }]} />
      <span className="scope-bar__slash">/</span>
      <Select aria-label="集群" size="small" placeholder="选择集群" value={activeClusterId || undefined}
        onChange={(value) => { void switchCluster(value) }} options={clusterOptions} />
      <span className="scope-bar__slash">/</span>
      <Select aria-label="命名空间" size="small" allowClear placeholder="命名空间" value={context.namespace || undefined}
        onChange={(value) => setNamespace(value || '')} options={context.namespace ? [{ value: context.namespace, label: context.namespace }] : []} />
      <span className="scope-bar__slash">/</span>
      <Select aria-label="资源" size="small" allowClear placeholder="资源" value={context.resource?.id}
        onChange={(value) => setResource(value ? { type: context.resource?.type || 'resource', id: value, label: value } : undefined)}
        options={context.resource ? [{ value: context.resource.id, label: context.resource.label }] : []} />
      <span className="scope-bar__slash">/</span>
      <Select aria-label="时间范围" size="small" value={timeValue} onChange={(value) => {
        if (value === 'absolute') return
        setTimeRange({ mode: 'relative', minutes: Number(value) as 15 | 60 | 360 | 1440 })
      }} options={[{ value: '15', label: '最近 15 分钟' }, { value: '60', label: '最近 1 小时' }, { value: '360', label: '最近 6 小时' }, { value: '1440', label: '最近 24 小时' }]} />
      <Space size={4} className="scope-bar__summary"><span>{context.environment}</span><span>{scopeLabel(activeClusterId, clusters)}</span><span>{formatTimeRange(context.timeRange)}</span></Space>
    </div>
  )
}

export default ScopeBar
