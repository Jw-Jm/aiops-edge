import React, { useEffect, useState } from 'react'
import { Badge, Button, Card, Col, Collapse, Descriptions, Row, Space, Steps, Tag, Typography } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import { getRun, listRunEvidences, listRunTools, RunTool } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import { ToolResultStatus } from '../../components/ToolResultStatus'

const { Text } = Typography

// P12.3：智能调查页——完整展示 scope/Intent/Plan DAG/ToolResult/Evidence/Hypothesis/
// contradiction/missing/root cause/unknown/action-risk-approval-execution-verification/timeline。
// 数据源：GET /api/v1/ai/runs/:id（真实数据源；无数据/失败显示空态，不降级伪造 DEMO）

interface InvestigationDetail {
  runId: string
  scope: { tenantId: string; clusterId: string; resourceId: string; time: string }
  intent: string
  plan: { step: string; tool: string; status: string }[]
  evidence: { id: string; type: string; source: string; reliability: number | string; fact: string }[]
  hypothesis: { id: string; claim: string; support: number; contradictions: string[]; missing: string[] }[]
  rootCause: string
  confidence: number
  action: { status: string; risk: string; approver: string | null; execution: string | null; verification: string | null }
}

const EMPTY_DETAIL: InvestigationDetail = {
  runId: '',
  scope: { tenantId: '', clusterId: '', resourceId: '', time: '' },
  intent: '—',
  plan: [],
  evidence: [],
  hypothesis: [],
  rootCause: 'unknown',
  confidence: 0,
  action: { status: 'created', risk: 'R0', approver: null, execution: null, verification: null },
}

const InvestigationDetailView: React.FC = () => {
  const { runId } = useParams<{ runId: string }>()
  const navigate = useNavigate()
  const [detail, setDetail] = useState<InvestigationDetail>(EMPTY_DETAIL)
  // C2-4：真实 Tool Activity（ai_tool_runs），不用图节点推断冒充。
  const [tools, setTools] = useState<RunTool[]>([])

  useEffect(() => {
    // P12：接真实 Run 详情 GET /api/v1/ai/runs/:id；无数据/API 失败保持空态（不伪造 DEMO，不自动创建 Run）
    if (!runId) return
    let cancelled = false
    // C2-4：拉取真实 ToolRun（只读工具执行事实）。
    listRunTools(runId)
      .then((resp) => { if (!cancelled && Array.isArray(resp.data?.tools)) setTools(resp.data.tools) })
      .catch(() => { if (!cancelled) setTools([]) })
    getRun(runId)
      .then(async (resp) => {
        const r = resp.data?.run
        if (!r || cancelled) return
        // Evidence Detail API（tenant+cluster+run 三元授权）：失败渲染为空，不伪造
        let evidence: InvestigationDetail['evidence'] = []
        try {
          const evResp = await listRunEvidences(runId, {
            tenant_id: r.tenant_id ?? '',
            cluster_id: r.primary_cluster_id ?? '',
          })
          if (!cancelled && Array.isArray(evResp.data?.evidences)) {
            // 后端条目为 RCA evidence_chain 原始 dict + evidence_id：
            // {layer, finding, ...} → 前端 {id, type, source, reliability, fact}
            evidence = evResp.data.evidences.map((e) => ({
              id: String(e.evidence_id ?? e.id ?? ''),
              type: String(e.type ?? e.layer ?? 'unknown'),
              source: String(e.source ?? 'rca'),
              reliability: (e.reliability as number | string) ?? '-',
              fact: String(e.fact ?? e.finding ?? ''),
            }))
          }
        } catch { /* 拉取失败 → 空态 */ }
        if (cancelled) return
        setDetail({
          runId: r.run_id,
          scope: {
            tenantId: r.tenant_id ?? '',
            clusterId: r.primary_cluster_id ?? '',
            resourceId: r.intent ?? 'investigation',
            time: r.created_at ?? '',
          },
          intent: r.intent ?? '—',
          plan: [],
          evidence,
          hypothesis: [],
          rootCause: r.root_cause ?? 'unknown',
          confidence: r.confidence ?? 0,
          action: { status: 'created', risk: 'R0', approver: null, execution: null, verification: null },
        })
      })
      .catch(() => { if (!cancelled) setDetail(EMPTY_DETAIL) })
    return () => { cancelled = true }
  }, [runId])

  const d = detail

  return (
    <div>
      <PageHeader
        title="智能调查"
        desc={`Run ${runId ?? d.runId}`}
        actions={<Button onClick={() => window.history.back()}>返回</Button>}
      />
      <Row gutter={16}>
        <Col span={24}>
          <Card title="Scope 与 Intent" size="small">
            <Descriptions size="small" column={3}>
              <Descriptions.Item label="Tenant">{d.scope.tenantId}</Descriptions.Item>
              <Descriptions.Item label="Cluster">{d.scope.clusterId}</Descriptions.Item>
              <Descriptions.Item label="Resource">{d.scope.resourceId}</Descriptions.Item>
            </Descriptions>
            <Text strong>Intent：</Text>
            <Text>{d.intent}</Text>
          </Card>
        </Col>
        <Col span={12}>
          <Card title="工具活动 (真实 ToolRun)" size="small"
            extra={<Tag color="blue">C2-4 只读事实</Tag>}>
            {/* C2-4：只展示真实 ai_tool_runs，不用图节点/计划步骤推断冒充真实工具调用。 */}
            {tools.length === 0 ? (
              <Text type="secondary">暂无真实 ToolRun（数据源为空，不伪造）</Text>
            ) : (
              <Steps size="small" direction="vertical" current={tools.length}
                items={tools.map((t) => ({
                  title: `${t.tool_name} · ${t.status}`,
                  description: `quality=${t.result_quality ?? '-'} eligible=${t.eligible_for_evidence ? 'Y' : 'N'}` +
                    (t.result_truncated ? ' truncated' : ''),
                  status: t.status === 'success' ? 'finish'
                    : (t.status === 'running' || t.status === 'partial' ? 'process' : 'error'),
                }))} />
            )}
          </Card>
        </Col>
        <Col span={12}>
          <Card title="Evidence" size="small">
            {d.evidence.map((e) => (
              <div key={e.id} style={{ marginBottom: 8, cursor: 'pointer' }}
                onClick={() => navigate(`/investigation/${runId ?? d.runId}/evidence/${encodeURIComponent(e.id)}`)}
                title="查看证据详情">
                <Space>
                  <Tag>{e.type}</Tag>
                  <Text>{e.source}</Text>
                  <Text type="secondary">rel {e.reliability}</Text>
                </Space>
                <div><Text>{e.fact}</Text></div>
              </div>
            ))}
          </Card>
          <Card title="执行状态（SSE 区分，不统一为暂无数据）" size="small" style={{ marginTop: 16 }}>
            <ToolResultStatus status="no_data" detail="数据源无匹配记录" />
            <ToolResultStatus status="permission_denied" detail="权限被拒绝，非数据缺失" />
            <ToolResultStatus status="unavailable" detail="数据源服务未就绪" />
            <ToolResultStatus status="timeout" detail="查询超时" />
          </Card>
        </Col>
        <Col span={24}>
          <Card title="Hypothesis / Root Cause / Action" size="small">
            {d.hypothesis.map((h) => (
              <Collapse key={h.id} size="small" items={[{
                key: h.id,
                label: <Space>{h.claim} <Tag color={h.support > 0.8 ? 'green' : 'blue'}>support {(h.support * 100).toFixed(0)}%</Tag></Space>,
                children: (
                  <Descriptions size="small" column={1}>
                    <Descriptions.Item label="根因">{d.rootCause}</Descriptions.Item>
                    <Descriptions.Item label="置信度">{(d.confidence * 100).toFixed(0)}%</Descriptions.Item>
                    <Descriptions.Item label="Action 状态">
                      <Badge status="warning" text={d.action.status} />
                      {' '}风险 <Tag>{d.action.risk}</Tag>
                    </Descriptions.Item>
                  </Descriptions>
                ),
              }]} />
            ))}
          </Card>
        </Col>
      </Row>
    </div>
  )
}

export default InvestigationDetailView
