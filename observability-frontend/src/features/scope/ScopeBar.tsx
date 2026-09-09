import React from 'react'
import { Select, Space, Tag } from 'antd'
import { Link } from 'react-router-dom'
import { useScopeStore } from '../../store/scopeStore'
import { formatTimeRange, type RunScopeSnapshot } from './types'
import { resourceLocation, resourceTypeLabel } from '../resources/resourceDomain'
import ResourcePicker from './ResourcePicker'

interface ScopeBarProps {
  snapshot?: RunScopeSnapshot
}

function scopeLabel(clusterId: string, clusters: Array<{ cluster_id: string; name: string }>): string {
  return clusters.find((cluster) => cluster.cluster_id === clusterId)?.name || clusterId || '未选择'
}

export function ScopeBar({ snapshot }: ScopeBarProps) {
  const active = useScopeStore((state) => state.active)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? state.active.clusterId)
  const clusters = useScopeStore((state) => state.clusters)
  const switchCluster = useScopeStore((state) => state.switchCluster)
  const switching = useScopeStore((state) => state.switching)
  const setResource = useScopeStore((state) => state.setResource)
  const setTimeRange = useScopeStore((state) => state.setTimeRange)

  if (snapshot) {
    const snapshotParts = [
      '生产平台',
      scopeLabel(snapshot.clusterId, clusters),
      snapshot.resource ? `${resourceTypeLabel(snapshot.resource.type)} · ${resourceLocation(snapshot.resource)}` : '集群范围',
      formatTimeRange(snapshot.timeRange),
    ]
    return (
      <div className="scope-bar scope-bar--snapshot" aria-label="调查快照">
        <Tag color="default">调查快照</Tag>
        <span>{snapshotParts.join(' / ')}</span>
        {snapshot.resource && <Link to={`/resources?resource=${encodeURIComponent(snapshot.resource.uid)}`}>在当前全局范围查看资源</Link>}
      </div>
    )
  }

  const clusterOptions = clusters.map((cluster) => ({ value: cluster.cluster_id, label: cluster.name }))
  const timeValue = active.timeRange.mode === 'relative' ? String(active.timeRange.minutes) : 'absolute'
  return (
    <div className="scope-bar" aria-label="活动作用域">
      <Tag color="blue">生产平台</Tag>
      <span className="scope-bar__slash">/</span>
      <Select aria-label="集群" size="small" placeholder="选择集群" value={activeClusterId || undefined}
        onChange={(value) => { void switchCluster(value) }} options={clusterOptions} />
      <span className="scope-bar__slash">/</span>
      <ResourcePicker clusterId={activeClusterId} value={active.resource} disabled={switching} onChange={setResource} />
      <span className="scope-bar__slash">/</span>
      <Select aria-label="时间范围" size="small" value={timeValue} onChange={(value) => {
        if (value === 'absolute') return
        setTimeRange({ mode: 'relative', minutes: Number(value) as 15 | 60 | 360 | 1440 })
      }} options={[{ value: '15', label: '最近 15 分钟' }, { value: '60', label: '最近 1 小时' }, { value: '360', label: '最近 6 小时' }, { value: '1440', label: '最近 24 小时' }]} />
      <Space size={4} className="scope-bar__summary"><span>{scopeLabel(activeClusterId, clusters)}</span><span>{active.resource ? resourceTypeLabel(active.resource.type) : '集群范围'}</span><span>{formatTimeRange(active.timeRange)}</span></Space>
    </div>
  )
}

export default ScopeBar
