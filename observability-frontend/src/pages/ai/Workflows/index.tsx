import React, { useEffect, useState } from 'react'
import { Button, Card, Col, Drawer, Input, Modal, Popconfirm, Row, Select, Space, Switch, Tag, Typography, message } from 'antd'
import { useNavigate } from 'react-router-dom'
import {
  listWorkflows, createWorkflow, deleteWorkflow, toggleWorkflow, runWorkflow, listFlowRuns,
  generateWorkflow, type FlowItem, type FlowRunItem, runStatusText, runStatusTone,
} from '../../../api/workflows'
import { PageHeader, Breadcrumb, PaneCard, StatusBadge, Empty } from '../../../components/ui/PageKit'
import AppIcon from '../../../components/AppIcons'

const { Text } = Typography

// =====================================================================
//  工作流列表：卡片网格（名称/启停 Switch/最近运行状态）
//  + 新建 / NL 生成 / 运行参数 Drawer（trigger payload JSON + 手动 run）
// =====================================================================

const Workflows: React.FC = () => {
  const navigate = useNavigate()
  const [flows, setFlows] = useState<FlowItem[]>([])
  const [loading, setLoading] = useState(true)
  const [latestRun, setLatestRun] = useState<Record<string, FlowRunItem>>({})
  const [detail, setDetail] = useState<FlowItem | null>(null)
  const [runOpen, setRunOpen] = useState(false)
  const [running, setRunning] = useState(false)
  const [triggerType, setTriggerType] = useState<'manual' | 'cron' | 'alert_fired'>('manual')
  const [triggerJson, setTriggerJson] = useState('{}')
  const [service, setService] = useState('')
  const [messageText, setMessageText] = useState('')
  const [runResult, setRunResult] = useState('')
  const [genOpen, setGenOpen] = useState(false)
  const [genPrompt, setGenPrompt] = useState('')
  const [genLoading, setGenLoading] = useState(false)
  const [genPreview, setGenPreview] = useState<{ name?: string; description?: string; graph?: { nodes: any[]; edges: any[] } } | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      const r = await listWorkflows()
      const list: FlowItem[] = r?.data?.flows || r?.data || []
      setFlows(list)
      // 拉取各工作流最近一次运行状态（容错失败）
      // TODO(B10): N+1 —— 每工作流一次 listFlowRuns 查询（约 46-50 行）。后端提供批量
      // 最新运行状态端点（如 GET /ai/workflows/runs/latest）或列表响应内嵌 latest_run
      // 后可移除，改为单次请求。
      const latest: Record<string, FlowRunItem> = {}
      await Promise.allSettled(list.map(async (f) => {
        const rr = await listFlowRuns(f.id)
        const runs: FlowRunItem[] = rr?.data?.runs || rr?.data || []
        if (runs[0]) latest[f.id] = runs[0]
      }))
      setLatestRun(latest)
    } catch {
      message.error('加载工作流失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const doCreate = async () => {
    const name = createName.trim() || '新建工作流'
    const graph = {
      nodes: [
        { id: 'n1', type: 'trigger.manual', name: '手动触发', config: {}, position: { x: 120, y: 80 } },
        { id: 'n2', type: 'collect', name: '数据采集', config: { service: '' }, position: { x: 360, y: 80 } },
        { id: 'n3', type: 'summarize', name: '汇总输出', config: {}, position: { x: 600, y: 80 } },
      ],
      edges: [
        { id: 'e1', source: 'n1', sourcePort: 'next', target: 'n2' },
        { id: 'e2', source: 'n2', sourcePort: 'next', target: 'n3' },
      ],
    }
    try {
      const r = await createWorkflow({ name, enabled: true, graph })
      const id = r?.data?.id
      message.success('新建工作流成功')
      setCreateOpen(false)
      if (id) {
        navigate(`/ai/workflows/editor?id=${encodeURIComponent(id)}`)
      } else {
        load()
      }
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '新建失败')
    }
  }

  const doToggle = async (f: FlowItem) => {
    try {
      await toggleWorkflow(f.id)
      message.success('状态已更新')
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '切换状态失败')
    }
  }

  const doDelete = async (id: string) => {
    try {
      await deleteWorkflow(id)
      message.success('删除成功')
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '删除失败')
    }
  }

  const openRun = (f: FlowItem) => {
    setDetail(f)
    setTriggerType('manual')
    setTriggerJson('{}')
    setService('')
    setMessageText('')
    setRunResult('')
    setRunOpen(true)
  }

  const doRun = async () => {
    if (!detail) return
    // 组装 trigger payload：手动 → {type:"manual",...}，cron → {type:"cron",cron}，alert → {type:"alert_fired",rule,min_severity}
    let extra: Record<string, unknown> = {}
    try {
      extra = triggerJson.trim() ? JSON.parse(triggerJson) : {}
    } catch {
      message.error('trigger JSON 不合法')
      return
    }
    const trigger: Record<string, unknown> = { type: triggerType, ...extra }
    setRunning(true)
    setRunResult('运行中...')
    try {
      const r = await runWorkflow(detail.id, { trigger, message: messageText || undefined, service: service || undefined })
      const res = r?.data
      setRunResult(typeof res === 'string' ? res : JSON.stringify(res, null, 2))
      message.success('运行已提交')
      load()
    } catch (e: any) {
      setRunResult(`运行失败: ${e?.response?.data?.detail || e?.response?.data?.error || e?.message || ''}`)
    } finally {
      setRunning(false)
    }
  }

  const doGenerate = async () => {
    if (!genPrompt.trim()) { message.warning('请输入需求描述'); return }
    setGenLoading(true)
    try {
      const r = await generateWorkflow({ prompt: genPrompt })
      setGenPreview(r?.data || null)
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '生成失败')
    } finally {
      setGenLoading(false)
    }
  }

  const applyGenerated = async () => {
    if (!genPreview?.graph) { message.warning('生成结果缺少 graph'); return }
    try {
      const r = await createWorkflow({
        name: genPreview.name || '生成工作流',
        enabled: true,
        graph: genPreview.graph,
      })
      const id = r?.data?.id
      message.success('已生成并创建工作流')
      setGenOpen(false)
      setGenPrompt('')
      setGenPreview(null)
      if (id) {
        navigate(`/ai/workflows/editor?id=${encodeURIComponent(id)}`)
      } else {
        load()
      }
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '创建失败')
    }
  }

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '工作流' }]} />
      <PageHeader title="工作流" desc="编排诊断与处置链路：触发器 / 采集 / 分析 / 审批 / 执行，支持手动、定时与告警触发"
        actions={
          <Space wrap>
            <Button icon={<AppIcon name="sparkles" />} onClick={() => { setGenOpen(true); setGenPreview(null); setGenPrompt('') }}>NL 生成</Button>
            <Button type="primary" icon={<AppIcon name="workflow" />} onClick={() => { setCreateName(''); setCreateOpen(true) }}>新建工作流</Button>
          </Space>
        } />

      <PaneCard>
        {flows.length === 0 && !loading ? (
          <Empty text="暂无工作流" hint="点击右上角「新建工作流」或「NL 生成」开始" />
        ) : (
          <Row gutter={[16, 16]}>
            {flows.map((f) => {
              const run = latestRun[f.id]
              return (
                <Col xs={24} md={12} xl={8} key={f.id}>
                  <Card
                    style={{ background: 'var(--surface-1)', borderColor: 'var(--border)', borderRadius: 12, height: '100%' }}
                    title={
                      <Space size={6}>
                        <AppIcon name="workflow" />
                        <span>{f.name || f.id}</span>
                      </Space>
                    }
                    extra={
                      <Switch checked={f.enabled !== false} size="small" onChange={() => doToggle(f)} />
                    }
                  >
                    <div style={{ color: 'var(--text-secondary)', fontSize: 12, marginBottom: 10, minHeight: 34 }}>
                      {f.description || '（无描述）'}
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 12 }}>
                      {f.graph?.nodes?.length ?? 0} 个节点 · {f.graph?.edges?.length ?? 0} 条边
                      <span style={{ marginLeft: 10 }}>· {f.enabled === false ? '已停用' : '已启用'}</span>
                    </div>
                    {run ? (
                      <div style={{ marginBottom: 12 }}>
                        <StatusBadge text={runStatusText[run.status] || run.status} tone={runStatusTone(run.status)} />
                        <span style={{ marginLeft: 8, fontSize: 11, color: 'var(--text-muted)' }}>
                          {run.trigger_type || 'manual'}{run.created_at ? ` · ${String(run.created_at).slice(5, 16).replace('T', ' ')}` : ''}
                        </span>
                      </div>
                    ) : (
                      <div style={{ marginBottom: 12, fontSize: 11, color: 'var(--text-muted)' }}>暂无运行记录</div>
                    )}
                    <Space wrap>
                      <Button size="small" icon={<AppIcon name="send" />} onClick={() => openRun(f)}>运行</Button>
                      <Button size="small" type="primary" onClick={() => navigate(`/ai/workflows/editor?id=${encodeURIComponent(f.id)}`)}>编辑</Button>
                      <Button size="small" onClick={() => navigate(`/ai/workflows/${encodeURIComponent(f.id)}`)}>历史</Button>
                      <Popconfirm title="确认删除该工作流？" onConfirm={() => doDelete(f.id)}>
                        <Button size="small" danger>删除</Button>
                      </Popconfirm>
                    </Space>
                  </Card>
                </Col>
              )
            })}
          </Row>
        )}
      </PaneCard>

      {/* ===== 运行参数 Drawer ===== */}
      <Drawer title={`运行：${detail?.name || ''}`} open={runOpen} onClose={() => setRunOpen(false)} width={560}
        styles={{ body: { padding: 16 } }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>触发器类型</Text>
            <Select style={{ width: '100%', marginTop: 4 }} value={triggerType}
              onChange={(v) => { setTriggerType(v); setTriggerJson('{}') }}
              options={[
                { value: 'manual', label: '手动触发' },
                { value: 'cron', label: '定时触发 (cron)' },
                { value: 'alert_fired', label: '告警触发' },
              ]} />
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>服务（可选）</Text>
            <Input style={{ marginTop: 4 }} value={service} onChange={(e) => setService(e.target.value)} placeholder="如 deepflow-server" />
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>触发消息（可选）</Text>
            <Input.TextArea style={{ marginTop: 4 }} rows={2} value={messageText} onChange={(e) => setMessageText(e.target.value)} placeholder="诊断诉求" />
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>trigger payload（JSON，可选）
              {triggerType === 'cron' && <span style={{ marginLeft: 6 }}>cron 节点配置在编辑器中</span>}
            </Text>
            <Input.TextArea style={{ marginTop: 4, fontFamily: 'var(--font-mono)', fontSize: 12 }} rows={4}
              value={triggerJson} onChange={(e) => setTriggerJson(e.target.value)}
              placeholder={triggerType === 'cron' ? '{"cron":"0 * * * *"}' : triggerType === 'alert_fired' ? '{"rule":"high-cpu","min_severity":"warning"}' : '{}'} />
          </div>
          <Button type="primary" icon={<AppIcon name="send" />} loading={running} onClick={doRun}>执行运行</Button>
          {runResult && (
            <pre style={{ background: 'var(--surface-2)', padding: 12, borderRadius: 8, fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 260, overflow: 'auto' }}>
              {runResult}
            </pre>
          )}
        </div>
      </Drawer>

      {/* ===== 新建工作流 ===== */}
      <Modal title="新建工作流" open={createOpen} onOk={doCreate} onCancel={() => setCreateOpen(false)} okText="创建并进入编辑器">
        <div style={{ marginTop: 12 }}>
          <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>名称</Text>
          <Input style={{ marginTop: 4 }} value={createName} onChange={(e) => setCreateName(e.target.value)} placeholder="新建工作流" />
        </div>
      </Modal>

      {/* ===== NL 生成 ===== */}
      <Modal title="NL 生成工作流" open={genOpen} onCancel={() => setGenOpen(false)}
        footer={genPreview ? (
          <Space>
            <Button onClick={() => setGenPreview(null)}>重新生成</Button>
            <Button type="primary" onClick={applyGenerated}>创建并进入编辑器</Button>
          </Space>
        ) : (
          <Button type="primary" loading={genLoading} onClick={doGenerate}>生成</Button>
        )}>
        {!genPreview ? (
          <div style={{ marginTop: 12 }}>
            <Input.TextArea rows={5} value={genPrompt} onChange={(e) => setGenPrompt(e.target.value)}
              placeholder={'用一句话描述工作流，如：\n"每小时采集 deepflow-server 指标，做 RCA 根因分析，若风险分超过 3 则进入人工审批，审批通过后执行重启，最后生成报告"'} />
            <div style={{ marginTop: 8, fontSize: 12, color: 'var(--text-muted)' }}>由 LLM 生成节点与连线，生成后可校验调整再保存。</div>
          </div>
        ) : (
          <div style={{ marginTop: 12 }}>
            <div style={{ fontWeight: 600, fontSize: 14 }}>{genPreview.name || '生成工作流'}</div>
            <div style={{ color: 'var(--text-secondary)', fontSize: 12, margin: '6px 0 10px' }}>{genPreview.description || ''}</div>
            <Tag color="blue">{genPreview.graph?.nodes?.length ?? 0} 个节点</Tag>
            <Tag color="green">{genPreview.graph?.edges?.length ?? 0} 条边</Tag>
            <div style={{ marginTop: 10, fontSize: 12, color: 'var(--text-muted)' }}>
              节点：{(genPreview.graph?.nodes || []).map((n) => n.name || n.type).join(' → ')}
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

export default Workflows
