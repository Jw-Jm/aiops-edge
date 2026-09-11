import React, { useState } from 'react'
import { Button, Card, Col, Collapse, Descriptions, Row, Space, Tag, Typography } from 'antd'
import type { InvestigationViewModel } from '../../features/investigation/model'
import GraphContextPanel from '../../components/graph/GraphContextPanel'
import RawDataPanel from '../../components/display/RawDataPanel'
import DataState from '../../components/display/DataState'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import { investigationStatusLabel, terminationReasonLabel } from '../../features/workflow/statusPresentation'

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
          <Descriptions.Item label="Run ID"><Typography.Text code copyable>{model.runId}</Typography.Text></Descriptions.Item>
        </Descriptions>
      </Space>
      <GraphContextPanel context={graphContext} />
      <div style={{ marginTop: 12 }}><Tag>状态：{investigationStatusLabel(model.status)}</Tag><Tag>证据：{model.evidence.length} 条</Tag></div>
    </Card>
  )
}

/** 完整证据时间线、工具活动与原始载荷：默认收进"技术详情"。 */
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

/** 结论头：只允许"根因已确认 / 根因尚未确认"两种主状态。 */
const ConclusionHeader: React.FC<{ model: InvestigationViewModel }> = ({ model }) => {
  const confirmed = model.summary.conclusionState === 'confirmed'
  const factors = model.summary.evidenceFactors
  return (
    <Card size="small" className="investigation-conclusion">
      <Space wrap align="center">
        <Tag color={confirmed ? 'green' : 'orange'}>{confirmed ? '根因已确认' : '根因尚未确认'}</Tag>
        <Typography.Title level={4} style={{ margin: 0 }}>{confirmed ? model.summary.rootCause : '尚未确认根因'}</Typography.Title>
      </Space>
      <div className="investigation-conclusion__factors">
        <span>置信度 {Math.round(model.conclusion.confidence * 100)}%</span>
        <span>支持 {factors.supporting}</span>
        <span>反驳 {factors.contradicting}</span>
        <span>缺失 {factors.missing}</span>
        <span>{factors.partial ? '数据部分可用' : factors.stale ? '数据已陈旧' : '数据完整'}</span>
      </div>
    </Card>
  )
}

/** 因果链：每跳一行"源资源 ─关系→ 目标资源"，推断边标注"推断"。 */
const CausalChain: React.FC<{ model: InvestigationViewModel }> = ({ model }) => {
  const [expanded, setExpanded] = useState(false)
  const chain = model.summary.causalChain
  if (chain.length === 0) return <DataState kind="empty" compact title="尚未形成可验证因果链" description="当前没有引用真实图边与证据的因果链" />
  const visible = expanded ? chain : chain.slice(0, 6)
  return (
    <div className="causal-chain">
      {visible.map((step, index) => (
        <div className="causal-chain__row" key={`${step.source}-${step.relation}-${step.target}-${index}`}>
          <span className="causal-chain__source">{step.source}</span>
          <span className="causal-chain__relation">{step.relation}<span aria-hidden="true"> →</span></span>
          <span className="causal-chain__target">{step.target}</span>
          <Tag color={step.factStatus === 'inferred' ? 'warning' : 'default'}>{step.factStatus === 'inferred' ? '推断' : '事实'}</Tag>
        </div>
      ))}
      {chain.length > 6 && <Button type="link" size="small" onClick={() => setExpanded((value) => !value)}>{expanded ? '收起因果链' : `展开完整因果链（共 ${chain.length} 跳）`}</Button>}
    </div>
  )
}

const KeyEvidence: React.FC<{ model: InvestigationViewModel; onEvidenceClick?: (id: string) => void }> = ({ model, onEvidenceClick }) => {
  const key = model.evidence.slice(0, 5)
  if (key.length === 0) return <DataState kind="empty" compact title="暂无关键证据" description="没有可引用的持久化证据" />
  return (
    <ul className="key-evidence">
      {key.map((item) => (
        <li key={item.id} className="key-evidence__item">
          <Space wrap size={4}>
            <Tag color="blue">{item.type || '证据'}</Tag>
            <Tag>{item.source}</Tag>
            <Tag color={item.reliability != null && item.reliability >= 0.85 ? 'green' : 'default'}>{item.reliability == null ? '可靠性未提供' : `可靠性 ${item.reliability}`}</Tag>
            {item.observedAt && <Typography.Text type="secondary">{absoluteTime(item.observedAt)}</Typography.Text>}
            {onEvidenceClick && <Button type="link" size="small" onClick={() => onEvidenceClick(item.id)}>查看原始数据</Button>}
          </Space>
          <div>{item.fact || '证据内容未提供'}</div>
        </li>
      ))}
    </ul>
  )
}

const InvestigationShell: React.FC<{ model: InvestigationViewModel; graphContext: Record<string, unknown> | null; tools: Array<{ tool_name: string; status: string; result_quality?: string; eligible_for_evidence?: boolean }>; onOpenAction?: () => void; onEvidenceClick?: (id: string) => void }> = ({ model, graphContext, tools, onOpenAction, onEvidenceClick }) => (
  <div className="investigation-summary-grid">
    <div className="investigation-summary-grid__main">
      {/* 顺序固定：结论 → 根因/因果链 → 关键证据 → 技术详情（默认收起）。 */}
      <ConclusionHeader model={model} />
      <Card title="根因与因果链" size="small" style={{ marginTop: 12 }}>
        <CausalChain model={model} />
      </Card>
      <Card title="关键事实证据" size="small" style={{ marginTop: 12 }}>
        <KeyEvidence model={model} onEvidenceClick={onEvidenceClick} />
      </Card>
      <Collapse
        style={{ marginTop: 12 }}
        items={[{
          key: 'technical',
          label: '技术详情（完整证据时间线、假设、工具活动与原始载荷）',
          children: <Row gutter={[16, 16]}>
            <Col xs={24}><EvidenceTimeline model={model} tools={tools} onEvidenceClick={onEvidenceClick} /></Col>
            <Col xs={24}><JudgementPane model={model} onOpenAction={onOpenAction} /></Col>
            <Col xs={24}><ImpactPane model={model} graphContext={graphContext} /></Col>
          </Row>,
        }]}
      />
    </div>
    <div className="investigation-summary-grid__side">
      <ImpactPane model={model} graphContext={graphContext} />
      <Card title="证据完整性" size="small" style={{ marginTop: 12 }}>
        <Descriptions size="small" column={1} items={[
          { key: 'supporting', label: '支持证据', children: model.summary.evidenceFactors.supporting },
          { key: 'contradicting', label: '反证', children: model.summary.evidenceFactors.contradicting },
          { key: 'missing', label: '缺失证据', children: model.summary.evidenceFactors.missing },
          { key: 'integrity', label: '数据完整性', children: model.summary.evidenceFactors.stale ? '数据已陈旧' : model.summary.evidenceFactors.partial ? '部分数据可用' : '完整' },
        ]} />
      </Card>
      <Card title="终止原因与预算" size="small" style={{ marginTop: 12 }}>
        <Typography.Paragraph style={{ marginBottom: 8 }}>{terminationReasonLabel(model.summary.terminationReason)}</Typography.Paragraph>
        <Descriptions size="small" column={1} items={[
          { key: 'steps', label: '步骤消耗', children: `${model.summary.budget.consumedSteps}/${model.summary.budget.maxSteps || '未提供'}` },
          { key: 'tools', label: '工具调用', children: `${model.summary.budget.consumedTools}/${model.summary.budget.maxTools || '未提供'}` },
        ]} />
      </Card>
      <Card title="下一步验证" size="small" style={{ marginTop: 12 }}>
        {model.summary.nextVerification.length
          ? <ul className="next-verification">{model.summary.nextVerification.map((item) => <li key={item}>{item}</li>)}</ul>
          : <Typography.Text type="secondary">尚未提供可验证的下一步</Typography.Text>}
        {onOpenAction && <Button style={{ marginTop: 8 }} onClick={onOpenAction}>创建处置建议 / 打开处置中心</Button>}
      </Card>
    </div>
  </div>
)

export default InvestigationShell
