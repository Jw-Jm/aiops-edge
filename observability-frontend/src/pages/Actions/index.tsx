import React from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import Approvals from '../admin/Approvals'
import K8sActions from '../infra/K8sActions'
import AiChat from '../ai/AiChat'

const Actions: React.FC = () => {
  const [params, setParams] = useSearchParams()
  const items = [
    { key: 'queue', label: '动作队列', children: <Approvals /> },
    { key: 'kubernetes', label: 'Kubernetes 处置', children: <K8sActions /> },
    { key: 'assistant', label: 'AI 处置建议', children: <AiChat /> },
  ]
  const requested = params.get('view') || 'queue'
  const activeKey = items.some((item) => item.key === requested) ? requested : 'queue'
  return (
    <div>
      <Breadcrumb items={[{ t: '处置' }, { t: '动作中心' }]} />
      <PageHeader title="动作中心" desc="所有环境变更均经过风险评估、审批、预检、执行与验证" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default Actions
