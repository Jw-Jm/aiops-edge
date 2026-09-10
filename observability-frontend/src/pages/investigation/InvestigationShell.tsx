import React from 'react'
import { Card, Col, Descriptions, Row, Space, Tag, Typography } from 'antd'
import type { InvestigationViewModel } from '../../features/investigation/model'
import GraphContextPanel from '../../components/graph/GraphContextPanel'
import RawDataPanel from '../../components/display/RawDataPanel'
import DataState from '../../components/display/DataState'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'

function absoluteTime(value?: string): string {
  return value ? value.replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC').replace(/Z$/, ' UTC') : '—'
}

export const ImpactPane: React.FC<{ model: InvestigationViewModel; graphContext: Record<string, unknown> | null }> = ({ model, graphContext }) => {
  const resource = model.scope.resource
  return (
    <Card title="冻结调查 Scope · 资源身份与影响面" size="small">
      <Space direction="vertical" size={4} style={{ width: '100%' }}>
        <Space wrap>
          <Tag color={resource ? 'blue' : 'default'}>{resource ? resourceTypeLabel(resource.type) : '集群范围'}</Tag>
          <Typography.Text strong>{resource ? resourceLocation(resource) : model.scope.clusterId || '未提供集群'}</Typography.Text>
        </Space>
        <Descriptions size="small" column={1}>
          <Descriptions.Item label="集群">{model.scope.clusterId || '—'}</Descriptions.Item>
          <Descriptions.Item label="冻结窗口">{model.scope.timeRange ? `${absoluteTime(model.scope.timeRange.start)} – ${absoluteTime(model.scope.timeRange.end)}` : '—'}</Descriptions.Item>
          <Descriptions.Item label="调查目标">{model.intent || '—'}</Descriptions.Item>
        </Descriptions>
      </Space>
      <GraphContextPanel context={graphContext} />
      <div style={{ marginTop: 12 }}><Tag>状态：{model.status}</Tag><Tag>证据：{model.evidence.length} 条</Tag></div>
    </Card>
  )
}

export const EvidenceTimeline: React.FC<{ model: InvestigationViewModel; tools: Array<{ tool_name: string; status: string; result_quality?: string; eligible_for_evidence?: boolean }>; onEvidenceClick?: (id: string) => void }> = ({ model, tools, onEvidenceClick }) => (
  <Card title="证据时间线" size="small">
    <div className="evidence-timeline">
      {model.evidence.length ? model.evidence.map((e) => (
        <div className="evidence-timeline__item" key={e.id} onClick={() => onEvidenceClick?.(e.id)} onKeyDown={(event) => { if (onEvidenceClick && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onEvidenceClick(e.id) } }} role={onEvidenceClick ? 'button' : undefined} tabIndex={onEvidenceClick ? 0 : undefined}>
          <div><Tag color="blue">事实证据</Tag><Tag>{e.source}</Tag><Tag color={e.reliability != null && e.reliability >= 0.8 ? 'green' : 'blue'}>{e.reliability == null ? '可靠性未提供' : `可靠性 ${e.reliability}`}</Tag><Tag>质量：{e.quality || '未提供'}</Tag></div>
          <Typography.Text>{e.fact || '证据内容未提供'}</Typography.Text>
          {e.observedAt && <div><Typography.Text type="secondary">观察时间：{absoluteTime(e.observedAt)}</Typography.Text></div>}
          {(e.supports.length > 0 || e.contradicts.length > 0) && <div className="evidence-timeline__links">{e.supports.map((item) => <Tag color="green" key={`s-${item}`}>支持：{item}</Tag>)}{e.contradicts.map((item) => <Tag color="red" key={`c-${item}`}>反驳：{item}</Tag>)}</div>}
        </div>
      )) : <DataState kind="empty" compact title="暂无持久化证据" description="当前不能形成确定性根因结论。" />}
    </div>
    {tools.length > 0 && <div style={{ marginTop: 16 }}><Typography.Text strong>工具活动</Typography.Text>{tools.map((tool, index) => <div key={`${tool.tool_name}-${index}`} style={{ marginTop: 6, fontSize: 12 }}><Tag>{tool.tool_name}</Tag><Tag>{tool.status}</Tag><Typography.Text type="secondary">{tool.result_quality || '质量未提供'} · {tool.eligible_for_evidence ? '可作为证据' : '不可作为证据'}</Typography.Text></div>)}</div>}
    <RawDataPanel data={model.evidence} title="证据原始载荷（只读）" />
  </Card>
)

export const JudgementPane: React.FC<{ model: InvestigationViewModel; onOpenAction?: () => void }> = ({ model, onOpenAction }) => (
  <Card title="AI 判断与下一步" size="small">
    <Typography.Title level={5} style={{ marginTop: 0 }}>{model.conclusion.title || '根因未知'}</Typography.Title>
    <Typography.Text type="secondary">置信度：{model.conclusion.confidence == null ? '未提供' : `${Math.round(model.conclusion.confidence * 100)}%`}</Typography.Text>
    {model.conclusion.state === 'insufficient_evidence' && <div className="insufficient-evidence" role="alert">证据不足：当前仅展示候选假设、反证和缺失证据，不能作为确定性根因或执行依据。</div>}
    <div style={{ marginTop: 16 }}>{model.hypotheses.length ? model.hypotheses.map((h) => <div key={h.id} style={{ marginBottom: 12 }}><Typography.Text strong><Tag color="orange">推断</Tag>{h.claim}</Typography.Text><div><Tag color={h.confidence >= 0.8 ? 'green' : 'blue'}>{Math.round(h.confidence * 100)}%</Tag>{h.contradicts.map((item) => <Tag color="red" key={`c-${item}`}>矛盾：{item}</Tag>)}{h.missing.map((item) => <Tag color="orange" key={`m-${item}`}>缺失：{item}</Tag>)}</div></div>) : <Typography.Text type="secondary">暂无持久化假设</Typography.Text>}</div>
    {model.action && <div style={{ marginTop: 12, borderTop: '1px solid var(--border-soft)', paddingTop: 12 }}><Typography.Text strong>处置动作</Typography.Text><div><Tag>{model.action.status}</Tag><Tag color="gold">风险：{model.action.risk || '未提供'}</Tag></div>{onOpenAction && <button type="button" className="link-button" onClick={onOpenAction}>打开动作中心</button>}</div>}
  </Card>
)

const InvestigationShell: React.FC<{ model: InvestigationViewModel; graphContext: Record<string, unknown> | null; tools: Array<{ tool_name: string; status: string; result_quality?: string; eligible_for_evidence?: boolean }>; onOpenAction?: () => void; onEvidenceClick?: (id: string) => void }> = ({ model, graphContext, tools, onOpenAction, onEvidenceClick }) => (
  <Row gutter={[16, 16]} className="investigation-grid">
    <Col xs={24} lg={8}><ImpactPane model={model} graphContext={graphContext} /></Col>
    <Col xs={24} lg={8}><EvidenceTimeline model={model} tools={tools} onEvidenceClick={onEvidenceClick} /></Col>
    <Col xs={24} lg={8}><JudgementPane model={model} onOpenAction={onOpenAction} /></Col>
  </Row>
)

export default InvestigationShell
