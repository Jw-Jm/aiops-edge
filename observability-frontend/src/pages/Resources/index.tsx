import React from 'react'
import { Tabs } from 'antd'
import { useParams, useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import ResourceRelationships from '../observability/ResourceRelationships'
import ResourceCenter from './ResourceCenter'
import ResourceDirectory from './ResourceDirectory'
import { useScopeStore } from '../../store/scopeStore'

const Resources: React.FC = () => {
  const { clusterId: routeClusterId } = useParams<{ clusterId: string }>()
  const [params, setParams] = useSearchParams()
  const setScopeResource = useScopeStore((state) => state.setResource)
  const active = useScopeStore((state) => state.active)
  const activeClusterId = routeClusterId || active.clusterId || useScopeStore.getState().authScope?.activeClusterId || ''
  const requestedGroup = params.get('group') === 'kubevirt' ? 'kubevirt' : 'containers'
  const requestedView = params.get('view') === 'graph' ? 'relationships' : (params.get('view') || requestedGroup)
  const activeKey = ['containers', 'kubevirt', 'relationships'].includes(requestedView) ? requestedView : 'containers'
  const views = [
    { key: 'containers', label: '容器资源', children: <ResourceDirectory clusterId={activeClusterId} group="containers" selected={active.resource} onSelect={setScopeResource} /> },
    { key: 'kubevirt', label: 'KubeVirt 虚拟机', children: <ResourceDirectory clusterId={activeClusterId} group="kubevirt" selected={active.resource} onSelect={setScopeResource} /> },
    { key: 'relationships', label: '资源关系', children: <ResourceRelationships /> },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '集群资源' }, { t: '资源载体' }]} />
      <PageHeader title="资源载体" desc="容器资源与 KubeVirt 虚拟机分层展示；磁盘和 NAD 仅在依赖关系中出现" />
      <ResourceCenter><Tabs activeKey={activeKey} items={views} onChange={(key) => setParams((current) => { current.delete('view'); if (key === 'relationships') current.set('view', 'graph'); else current.set('group', key); return current })} destroyOnHidden /></ResourceCenter>
    </div>
  )
}

export default Resources
