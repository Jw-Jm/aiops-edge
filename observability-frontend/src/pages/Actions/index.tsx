import React from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import ActionCenter from './ActionCenter'
import K8sActions from '../infra/K8sActions'
import AiChat from '../ai/AiChat'

const Actions: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const items = [
    { key: 'queue', label: '动作队列', children: <ActionCenter /> },
    { key: 'kubernetes', label: 'Kubernetes 处置', children: <K8sActions /> },
    { key: 'assistant', label: 'AI 处置建议', children: <AiChat /> },
  ]
  const requested = params.get('view') || 'queue'
  const activeKey = items.some((item) => item.key === requested) ? requested : 'queue'
  return (
    <div>
      <Breadcrumb items={[{ t: '处置' }, { t: '动作中心' }]} />
      <PageHeader title="处置中心" desc="当前集群的全部处置动作，均经过风险评估、预检、审批、执行与验证" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default Actions
