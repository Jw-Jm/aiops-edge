import React from 'react'
import GraphOpsPanel from '../../components/graph/GraphOpsPanel'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'

export default function GraphOperations() {
  return <div><Breadcrumb items={[{ t: '系统管理' }, { t: '图谱同步' }]} /><PageHeader title="图谱同步" desc="查看同步、Outbox、别名和 shadow diff；图谱陈旧不等同于集群故障" /><GraphOpsPanel /></div>
}
