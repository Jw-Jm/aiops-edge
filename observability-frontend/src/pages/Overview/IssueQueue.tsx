import React from 'react'
import { Alert, Button, Card, Empty, Space, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { issueAction, type OperationalIssue } from './priority'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'

const IssueQueue: React.FC<{ issues: OperationalIssue[]; loading?: boolean; error?: string; onRetry?: () => void }> = ({ issues, loading, error, onRetry }) => {
  const navigate = useNavigate()
  const primary = issues[0]
  const secondary = issues.slice(1, 5)
  return (
    <section aria-label="当前最重要的问题" data-testid="issue-queue">
      <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>当前最重要的问题</Typography.Title><Typography.Text type="secondary">按严重度、影响范围和持续时间排序</Typography.Text></div><Button type="link" onClick={() => navigate('/observe?view=problems')}>查看全部问题 →</Button></div>
      {error ? <Alert type="error" showIcon message={error} action={<Button size="small" onClick={onRetry}>重试</Button>} /> : loading ? <Card loading size="small" /> : primary ? (
        <Card size="small" className="priority-issue-card">
          <div className="priority-issue-card__top"><Space><Tag color={primary.severity === 'critical' ? 'red' : primary.severity === 'warning' ? 'orange' : 'blue'}>{primary.severity === 'critical' ? '严重' : primary.severity === 'warning' ? '警告' : '信息'}</Tag><Typography.Text type="secondary">{primary.startedAt || '时间未提供'}</Typography.Text></Space><Button type="primary" onClick={() => navigate(issueAction(primary).href)}>{issueAction(primary).label}</Button></div>
          <Typography.Title level={5} style={{ margin: '10px 0 4px' }}>{primary.title}</Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>{primary.symptom}</Typography.Paragraph>
          <div className="priority-issue-card__meta"><span>范围：{primary.resource ? `${resourceTypeLabel(primary.resource.type)} · ${resourceLocation(primary.resource)}` : '集群范围'}</span><span>影响资源：{primary.impactCount ?? primary.affectedServices} 个</span><span>持续：{primary.durationMinutes > 0 ? `${primary.durationMinutes} 分钟` : '未提供'}</span><span>数据：{primary.dataStatus === 'partial' ? '部分数据' : primary.dataStatus === 'stale' ? '数据陈旧' : primary.dataStatus === 'unavailable' ? '数据不可用' : '数据正常'}</span>{primary.runId && <span>Run：{primary.runId}</span>}</div>
        </Card>
        ) : <Card size="small"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前作用域暂无活跃问题" /></Card>}
      {secondary.length > 0 && <Card size="small" style={{ marginTop: 10 }} className="priority-issue-list">
        {secondary.map((issue) => {
          const action = issueAction(issue)
          return <div key={issue.id} className="priority-issue-list__item">
            <div style={{ minWidth: 0, flex: 1 }}>
              <Space size={6}><Tag color={issue.severity === 'critical' ? 'red' : issue.severity === 'warning' ? 'orange' : 'blue'}>{issue.severity === 'critical' ? '严重' : issue.severity === 'warning' ? '警告' : '信息'}</Tag><Typography.Text strong ellipsis>{issue.title}</Typography.Text></Space>
              <div className="priority-issue-list__meta">{issue.resource ? `${resourceTypeLabel(issue.resource.type)} · ${resourceLocation(issue.resource)}` : '集群范围'} · 影响 {issue.impactCount ?? issue.affectedServices} 个资源 · 持续 {issue.durationMinutes > 0 ? `${issue.durationMinutes} 分钟` : '未提供'}{issue.startedAt ? ` · ${issue.startedAt}` : ''}</div>
            </div>
            <Button type="link" size="small" onClick={() => navigate(action.href)}>{action.label}</Button>
          </div>
        })}
      </Card>}
    </section>
  )
}

export default IssueQueue
