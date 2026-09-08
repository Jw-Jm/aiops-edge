import React from 'react'
import { Alert, Button, Card, Empty, Space, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { issueAction, type OperationalIssue } from './priority'

const IssueQueue: React.FC<{ issues: OperationalIssue[]; loading?: boolean; error?: string; onRetry?: () => void }> = ({ issues, loading, error, onRetry }) => {
  const navigate = useNavigate()
  const primary = issues[0]
  return (
    <section aria-label="当前最重要的问题" data-testid="issue-queue">
      <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>当前最重要的问题</Typography.Title><Typography.Text type="secondary">按严重度、影响范围和持续时间排序</Typography.Text></div><Button type="link" onClick={() => navigate('/observe?view=problems')}>查看全部问题 →</Button></div>
      {error ? <Alert type="error" showIcon message={error} action={<Button size="small" onClick={onRetry}>重试</Button>} /> : loading ? <Card loading size="small" /> : primary ? (
        <Card size="small" className="priority-issue-card">
          <div className="priority-issue-card__top"><Space><Tag color={primary.severity === 'critical' ? 'red' : primary.severity === 'warning' ? 'orange' : 'blue'}>{primary.severity === 'critical' ? '严重' : primary.severity === 'warning' ? '警告' : '信息'}</Tag><Typography.Text type="secondary">{primary.startedAt || '时间未提供'}</Typography.Text></Space><Button type="primary" onClick={() => navigate(issueAction(primary).href)}>{issueAction(primary).label}</Button></div>
          <Typography.Title level={5} style={{ margin: '10px 0 4px' }}>{primary.title}</Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>{primary.symptom}</Typography.Paragraph>
          <div className="priority-issue-card__meta"><span>影响资源：{primary.affectedServices} 个</span><span>持续：{primary.durationMinutes > 0 ? `${primary.durationMinutes} 分钟` : '未提供'}</span>{primary.runId && <span>Run：{primary.runId}</span>}</div>
        </Card>
      ) : <Card size="small"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前作用域暂无活跃问题" /></Card>}
    </section>
  )
}

export default IssueQueue
