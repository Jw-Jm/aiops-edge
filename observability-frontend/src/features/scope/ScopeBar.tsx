import React from 'react'
import { Select, Tag } from 'antd'
import { Link } from 'react-router-dom'
import { useScopeStore } from '../../store/scopeStore'
import { formatTimeRange, type RunScopeSnapshot } from './types'
import { resourceLocation, resourceTypeLabel } from '../resources/resourceDomain'

interface ScopeBarProps {
  snapshot?: RunScopeSnapshot
}

function scopeLabel(clusterId: string, clusters: Array<{ cluster_id: string; name: string }>): string {
  return clusters.find((cluster) => cluster.cluster_id === clusterId)?.name || clusterId || '未选择'
}

export function ScopeBar({ snapshot }: ScopeBarProps) {
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? state.active.clusterId)
  const clusters = useScopeStore((state) => state.clusters)
  const switchCluster = useScopeStore((state) => state.switchCluster)

  if (snapshot) {
    const snapshotParts = [
      scopeLabel(snapshot.clusterId, clusters),
      snapshot.resource ? `${resourceTypeLabel(snapshot.resource.type)} · ${resourceLocation(snapshot.resource)}` : '集群范围',
      formatTimeRange(snapshot.timeRange),
    ]
    return (
      <div className="scope-bar scope-bar--snapshot" aria-label="调查快照">
        <Tag color="default">调查快照</Tag>
        <span>{snapshotParts.join(' / ')}</span>
        {snapshot.resource && <Link to={`/clusters/${encodeURIComponent(snapshot.clusterId)}/resources?resource=${encodeURIComponent(snapshot.resource.uid)}`}>查看资源</Link>}
      </div>
    )
  }

  const clusterOptions = clusters.map((cluster) => ({ value: cluster.cluster_id, label: cluster.name }))
  return (
    <div className="scope-bar" aria-label="活动作用域">
      <span className="scope-bar__label">集群</span>
      {/* Select 是集群名称的唯一常驻显示，不再重复渲染一段同名摘要。 */}
      <Select aria-label="集群" size="small" placeholder="选择集群" value={activeClusterId || undefined}
        onChange={(value) => { void switchCluster(value) }} options={clusterOptions} />
    </div>
  )
}

export default ScopeBar
