import React from 'react'
import { Alert, Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { Breadcrumb, PageHeader } from '../../components/ui/PageKit'
import AdminUsers from './AdminUsers'
import AdminSettings, { AdminCapabilityOverview } from './AdminSettings'
import GraphOperations from './GraphOperations'
import { useAuthStore } from '../../store/authStore'

const AdminHome: React.FC = () => {
  const role = useAuthStore((state) => state.role)
  const [params, setParams] = useSearchParams()
  const items = [
    { key: 'overview', label: '能力总览', children: <AdminCapabilityOverview /> },
    { key: 'users', label: '用户与权限', children: <AdminUsers /> },
    { key: 'settings', label: '接入与能力配置', children: <AdminSettings /> },
    { key: 'graph', label: '图谱运维', children: <GraphOperations /> },
  ]
  const requested = params.get('view') || 'overview'
  const activeKey = items.some((item) => item.key === requested) ? requested : 'overview'
  if (role !== 'admin') return <Alert type="error" showIcon message="PermissionDenied" description="当前账号没有系统管理权限。" />
  return (
    <div>
      <Breadcrumb items={[{ t: '系统管理' }, { t: '平台管理' }]} />
      <PageHeader title="系统管理" desc="AIOps 自身健康、数据可信度与平台能力配置" />
      <Tabs activeKey={activeKey} items={items} onChange={(key) => setParams({ view: key })} destroyOnHidden />
    </div>
  )
}

export default AdminHome
