import React from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import Capacity from '../capacity/Capacity'
import ResourceRelationships from '../observability/ResourceRelationships'
import ResourceCenter from './ResourceCenter'
import ClusterPanorama from './ClusterPanorama'
import ResourceDirectory from './ResourceDirectory'
import { useScopeStore } from '../../store/scopeStore'
import type { ResourceDomain } from '../../features/resources/types'

const viewDefinitions: Array<{ key: string; label: string; domain?: ResourceDomain; children?: React.ReactNode }> = [
  { key: 'panorama', label: '集群全景' },
  { key: 'compute', label: '计算', domain: 'compute' },
  { key: 'network', label: '网络', domain: 'network' },
  { key: 'storage', label: '存储', domain: 'storage' },
  { key: 'kubernetes', label: 'Kubernetes', domain: 'kubernetes' },
  { key: 'application', label: '应用服务', domain: 'application' },
  { key: 'capacity', label: '容量', children: <Capacity /> },
  { key: 'relationships', label: '关系探索', children: <ResourceRelationships /> },
]

const Resources: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const setScopeResource = useScopeStore((state) => state.setResource)
  const active = useScopeStore((state) => state.active)
  const activeClusterId = active.clusterId || useScopeStore.getState().authScope?.activeClusterId || ''
  const requested = params.get('view') || 'panorama'
  const activeKey = viewDefinitions.some((view) => view.key === requested) ? requested : 'panorama'
  const views = viewDefinitions.map((view) => ({
    key: view.key,
    label: view.label,
    children: view.key === 'panorama'
      ? <ClusterPanorama clusterId={activeClusterId} />
      : view.domain
        ? <ResourceDirectory clusterId={activeClusterId} domain={view.domain} selected={active.resource} onSelect={setScopeResource} />
        : view.children,
  }))

  return (
    <div>
      <Breadcrumb items={[{ t: '资源' }, { t: '资源中心' }]} />
      <PageHeader title="资源中心" desc="以资源为主线查看服务、集群、节点与依赖关系" />
      <ResourceCenter><Tabs
          activeKey={activeKey}
          items={views}
          onChange={(key) => setParams((current) => { current.set('view', key); return current })}
          destroyOnHidden
        /></ResourceCenter>
    </div>
  )
}

export default Resources
