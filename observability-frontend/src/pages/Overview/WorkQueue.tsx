import React from 'react'
import { Badge, Button, Card, Empty, Space, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import type { ActionProjection, RunSummary } from '../../api/client'

const WorkQueue: React.FC<{ runs: RunSummary[]; actions: ActionProjection[]; loading?: boolean }> = ({ runs, actions, loading }) => {
  const navigate = useNavigate()
  const work = [
    ...runs.filter((run) => !['success', 'failed', 'cancelled'].includes(run.status)).slice(0, 3).map((run) => ({ key: `run-${run.run_id}`, kind: '调查', title: run.intent || run.target_resource_id || '调查', status: run.status, onClick: () => navigate(`/investigation/${run.run_id}`) })),
    ...actions.filter((action) => ['proposed', 'approved'].includes(action.status)).slice(0, 3).map((action) => ({ key: `action-${action.action_id}`, kind: '动作', title: `${action.operation} ${action.target_name}`, status: action.status, onClick: () => navigate('/actions') })),
  ]
  return <section aria-label="需要我处理" data-testid="work-queue"><div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>需要我处理</Typography.Title><Typography.Text type="secondary">调查进展和待审批动作</Typography.Text></div><Button type="link" onClick={() => navigate('/actions')}>打开动作中心 →</Button></div><Card size="small" loading={loading}>{work.length ? <Space direction="vertical" size={0} style={{ width: '100%' }}>{work.map((item) => <div key={item.key} className="work-queue__item" onClick={item.onClick}><Space><Tag>{item.kind}</Tag><Typography.Text ellipsis>{item.title}</Typography.Text></Space><Badge status={item.status === 'proposed' ? 'warning' : 'processing'} text={item.status} /></div>)}</Space> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无待处理事项" />}</Card></section>
}

export default WorkQueue
