import React from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import ServiceObservability from '../observability/ServiceObservability'
import K8sActions from '../infra/K8sActions'
import VirtualMachines from '../observability/VirtualMachines'
import Hardware from '../infra/Hardware'
import Capacity from '../capacity/Capacity'
import ResourceRelationships from '../observability/ResourceRelationships'

const views = [
  { key: 'services', label: '服务', children: <ServiceObservability /> },
  { key: 'kubernetes', label: 'Kubernetes', children: <K8sActions /> },
  { key: 'vms', label: '虚拟机', children: <VirtualMachines /> },
  { key: 'hardware', label: '硬件', children: <Hardware /> },
  { key: 'capacity', label: '容量', children: <Capacity /> },
  { key: 'relationships', label: '关系', children: <ResourceRelationships /> },
]

const Resources: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const requested = params.get('view') || 'services'
  const activeKey = views.some((view) => view.key === requested) ? requested : 'services'

  return (
    <div>
      <Breadcrumb items={[{ t: '资源' }, { t: '资源中心' }]} />
      <PageHeader title="资源中心" desc="以资源为主线查看服务、集群、节点与依赖关系" />
      <Tabs
        activeKey={activeKey}
        items={views}
        onChange={(key) => setParams({ view: key })}
        destroyOnHidden
      />
    </div>
  )
}

export default Resources
