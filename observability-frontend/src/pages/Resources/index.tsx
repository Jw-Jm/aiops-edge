import React, { useEffect } from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import ServiceObservability from '../observability/ServiceObservability'
import K8sActions from '../infra/K8sActions'
import VirtualMachines from '../observability/VirtualMachines'
import Hardware from '../infra/Hardware'
import Capacity from '../capacity/Capacity'
import ResourceRelationships from '../observability/ResourceRelationships'
import ResourceCenter from './ResourceCenter'
import { useScopeStore } from '../../store/scopeStore'

const views = [
  { key: 'services', label: '服务', children: <ServiceObservability embedded /> },
  { key: 'kubernetes', label: 'Kubernetes', children: <K8sActions /> },
  { key: 'vms', label: '虚拟机', children: <VirtualMachines /> },
  { key: 'hardware', label: '硬件', children: <Hardware /> },
  { key: 'capacity', label: '容量', children: <Capacity /> },
  { key: 'relationships', label: '关系', children: <ResourceRelationships /> },
]

const Resources: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const setScopeResource = useScopeStore((state) => state.setResource)
  const kind = params.get('kind')
  const resourceId = params.get('resource')
  const kindView: Record<string, string> = { service: 'services', vm: 'vms', kubernetes: 'kubernetes', hardware: 'hardware' }
  const requested = params.get('view') || (kind ? kindView[kind] : undefined) || 'services'
  const activeKey = views.some((view) => view.key === requested) ? requested : 'services'

  // Investigation snapshots intentionally link back to the current global
  // scope. Consume the resource query explicitly so the identity/chain panes
  // open on that resource instead of silently selecting the first row.
  useEffect(() => {
    if (!resourceId) return
    const type = kind === 'resource' || !kind ? 'service' : kind
    setScopeResource({ type, id: resourceId, label: resourceId })
  }, [kind, resourceId, setScopeResource])

  return (
    <div>
      <Breadcrumb items={[{ t: '资源' }, { t: '资源中心' }]} />
      <PageHeader title="资源中心" desc="以资源为主线查看服务、集群、节点与依赖关系" />
      <ResourceCenter><Tabs
          activeKey={activeKey}
          items={views}
          onChange={(key) => setParams({ view: key })}
          destroyOnHidden
        /></ResourceCenter>
    </div>
  )
}

export default Resources
