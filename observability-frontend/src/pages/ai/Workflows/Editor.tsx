import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  Handle,
  Position,
  addEdge,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type NodeProps,
  type Connection,
  type NodeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Button, Drawer, Modal, Tag, Input, InputNumber, Select, Space, Typography, message, Popconfirm, Empty } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  listWorkflowNodeTypes, getWorkflow, createWorkflow, updateWorkflow, runWorkflow,
  listFlowRuns, getFlowRun, resumeFlowRun, generateWorkflow, testFlowNode,
  type NodeSpec, type NodeConfigField, type FlowGraphWire, runStatusText, parseRunOutput,
} from '../../../api/workflows'
import { PageHeader, Breadcrumb } from '../../../components/ui/PageKit'
import AppIcon from '../../../components/AppIcons'

const { Text } = Typography

// =====================================================================
//  工作流编辑器：React Flow 画布（@xyflow/react 12.x）
//  - 节点面板数据驱动自 GET /node-types（按 config_fields 渲染表单）
//  - 节点按 kind 着色：trigger/action/control/data 四色
//  - 边 sourcePort 下拉（next/true/false/approved/rejected/error）
//  - 保存 PUT / NL 生成 / 单节点 test-node 试跑 / 运行 Drawer
// =====================================================================

// kind → 配色
const KIND_STYLE: Record<string, { border: string; bg: string; dot: string }> = {
  trigger: { border: 'var(--success)', bg: 'var(--success-soft)', dot: 'var(--success)' },
  action: { border: 'var(--primary)', bg: 'var(--primary-soft)', dot: 'var(--primary)' },
  control: { border: 'var(--warning)', bg: 'var(--warning-soft)', dot: 'var(--warning)' },
  data: { border: 'var(--info)', bg: 'var(--primary-soft)', dot: 'var(--info)' },
}

const RUN_STYLE: Record<string, { border: string; bg: string; color: string }> = {
  succeeded: { border: 'var(--success)', bg: 'var(--success-soft)', color: 'var(--success)' },
  failed: { border: 'var(--danger)', bg: 'var(--danger-soft)', color: 'var(--danger)' },
  running: { border: 'var(--primary)', bg: 'var(--primary-soft)', color: 'var(--primary)' },
  waiting_approval: { border: 'var(--warning)', bg: 'var(--warning-soft)', color: 'var(--warning)' },
  pending: { border: 'var(--text-muted)', bg: 'var(--surface-3)', color: 'var(--text-muted)' },
}

interface FlowNodeData {
  spec?: NodeSpec
  config: Record<string, unknown>
  name: string
  runStatus?: string
  output?: unknown
  firedPort?: unknown
  nodeError?: unknown
  [key: string]: unknown
}

type FlowNode = Node<FlowNodeData>

// ===== 输出端口（含 error 兜底）=====
function outputPorts(spec?: NodeSpec): string[] {
  if (!spec) return ['next', 'error']
  const outs = (spec.ports || [])
    .map((p) => (typeof p === 'string' ? p : p.name))
    .filter(Boolean)
  if (outs.length === 0) return ['next', 'error']
  if (!outs.includes('error')) return [...outs, 'error']
  return outs
}

// 边 sourcePort 候选值
const EDGE_PORT_OPTIONS = ['next', 'true', 'false', 'approved', 'rejected', 'error']

// ===== 自定义节点组件 =====
function FlowNodeComponent({ data, selected }: NodeProps<FlowNode>) {
  const spec = data.spec
  const kindStyle = KIND_STYLE[spec?.kind || 'action'] || KIND_STYLE.action
  const runStyle = data.runStatus ? RUN_STYLE[data.runStatus] : undefined
  const label = data.name || spec?.label || 'Node'
  const outs = outputPorts(spec)

  return (
    <div
      style={{
        minWidth: 170,
        background: runStyle ? runStyle.bg : 'var(--surface-1)',
        border: `1.5px solid ${runStyle ? runStyle.border : selected ? 'var(--primary)' : kindStyle.border}`,
        borderRadius: 10,
        padding: '10px 14px',
        boxShadow: '0 4px 14px rgba(0,0,0,0.08)',
        position: 'relative',
      }}
    >
      <Handle type="target" position={Position.Left} id="in" style={{ background: 'var(--text-muted)' }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, justifyContent: 'center' }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: kindStyle.dot, flexShrink: 0 }} />
        <div style={{ fontSize: 12, color: 'var(--text)', fontWeight: 600, textAlign: 'center', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {label}
        </div>
      </div>
      {data.runStatus && (
        <div style={{ fontSize: 10, color: runStyle?.color, textAlign: 'center', marginTop: 4 }}>
          {runStatusText[data.runStatus] || data.runStatus}
        </div>
      )}
      {outs.map((p, i) => {
        const isError = p === 'error'
        return (
          <Handle
            key={p}
            type="source"
            position={Position.Right}
            id={p}
            style={{
              background: isError ? 'var(--danger)' : kindStyle.dot,
              top: `${(i + 1) * (100 / (outs.length + 1))}%`,
            }}
          />
        )
      })}
    </div>
  )
}

const nodeTypes: NodeTypes = { flow: FlowNodeComponent }

// ===== 主编辑器 =====
const WorkflowEditor: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const flowId = searchParams.get('id') || undefined

  const [nodeTypesMap, setNodeTypesMap] = useState<Record<string, NodeSpec>>({})
  const [nodeTypesList, setNodeTypesList] = useState<NodeSpec[]>([])
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [selectedNode, setSelectedNode] = useState<FlowNode | null>(null)
  const [selectedEdge, setSelectedEdge] = useState<Edge | null>(null)
  const [flowName, setFlowName] = useState('未命名流程')
  const [flowDesc, setFlowDesc] = useState('')
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [runStatus, setRunStatus] = useState('')
  const [runId, setRunId] = useState('')
  const [runDrawerOpen, setRunDrawerOpen] = useState(false)
  const [runService, setRunService] = useState('')
  const [runMessage, setRunMessage] = useState('')
  const [runResult, setRunResult] = useState('')
  const [runs, setRuns] = useState<any[]>([])
  const [runsOpen, setRunsOpen] = useState(false)
  const [approval, setApproval] = useState<{ runId: string; output: any } | null>(null)
  const [genOpen, setGenOpen] = useState(false)
  const [genPrompt, setGenPrompt] = useState('')
  const [genLoading, setGenLoading] = useState(false)
  const [genPreview, setGenPreview] = useState<{ name?: string; description?: string; graph?: FlowGraphWire } | null>(null)
  const [testLoading, setTestLoading] = useState(false)
  const [testResult, setTestResult] = useState('')
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const flowIdRef = useRef(flowId)
  flowIdRef.current = flowId

  // 加载节点类型（数据驱动面板）
  useEffect(() => {
    listWorkflowNodeTypes()
      .then((r) => {
        const list: NodeSpec[] = r?.data?.node_types || r?.data || []
        const map: Record<string, NodeSpec> = {}
        list.forEach((nt) => { if (nt?.type) map[nt.type] = nt })
        setNodeTypesList(list)
        setNodeTypesMap(map)
      })
      .catch(() => message.error('加载节点类型失败'))
  }, [])

  // 加载流程（编辑模式）
  useEffect(() => {
    if (!flowId) return
    let cancelled = false
    const load = async () => {
      try {
        const r = await getWorkflow(flowId)
        const flow = r?.data?.flow || r?.data || {}
        if (cancelled) return
        setFlowName(flow.name || '未命名流程')
        setFlowDesc(flow.description || '')
        const g = flow.graph || {}
        const fnodes: FlowNode[] = (g.nodes || []).map((n: any) => ({
          id: n.id,
          type: 'flow',
          position: n.position || { x: 120, y: 80 },
          data: { spec: nodeTypesMap[n.type], config: n.config || {}, name: n.name || n.type },
        }))
        const fedges: Edge[] = (g.edges || []).map((e: any) => ({
          id: e.id,
          source: e.source,
          sourceHandle: e.sourcePort || undefined,
          target: e.target,
          targetHandle: 'in',
        }))
        setNodes(fnodes)
        setEdges(fedges)
      } catch {
        if (!cancelled) message.error('加载流程失败')
      }
    }
    load()
    listFlowRuns(flowId)
      .then((r) => { if (!cancelled) setRuns(r?.data?.runs || r?.data || []) })
      .catch(() => { /* optional */ })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flowId])

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  useEffect(() => () => stopPolling(), [stopPolling])

  // ===== 节点面板（按 kind 分组）=====
  const paletteGroups = useMemo(() => {
    const groups: Record<string, NodeSpec[]> = {}
    nodeTypesList.forEach((nt) => {
      const cat = nt.kind || '其他'
      if (!groups[cat]) groups[cat] = []
      groups[cat].push(nt)
    })
    return groups
  }, [nodeTypesList])

  const addNode = useCallback(
    (spec: NodeSpec) => {
      const id = `n${Date.now()}${Math.floor(Math.random() * 1000)}`
      const position = { x: 120 + nodes.length * 24, y: 80 + nodes.length * 24 }
      const defaults: Record<string, unknown> = {}
      ;(spec.config_fields || []).forEach((f) => {
        if (f.default !== undefined) defaults[f.name] = f.default
      })
      const node: FlowNode = {
        id,
        type: 'flow',
        position,
        data: { spec, config: defaults, name: spec.label || spec.type },
      }
      setNodes((nds) => nds.concat(node))
    },
    [nodes.length, setNodes]
  )

  // ===== 连线 =====
  const onConnect = useCallback(
    (conn: Connection) => {
      setEdges((eds) =>
        addEdge(
          {
            ...conn,
            sourceHandle: conn.sourceHandle || 'next',
            targetHandle: 'in',
          },
          eds
        )
      )
    },
    [setEdges]
  )

  const onNodeClick = useCallback((_: any, node: FlowNode) => {
    setSelectedNode(node)
    setSelectedEdge(null)
  }, [])

  const onEdgeClick = useCallback((_: any, edge: Edge) => {
    setSelectedEdge(edge)
    setSelectedNode(null)
  }, [])

  const onPaneClick = useCallback(() => {
    setSelectedNode(null)
    setSelectedEdge(null)
    setTestResult('')
  }, [])

  const deleteSelected = useCallback(() => {
    if (!selectedNode) return
    setNodes((nds) => nds.filter((n) => n.id !== selectedNode.id))
    setEdges((eds) => eds.filter((e) => e.source !== selectedNode.id && e.target !== selectedNode.id))
    setSelectedNode(null)
  }, [selectedNode, setNodes, setEdges])

  const updateEdgePort = useCallback(
    (port: string) => {
      if (!selectedEdge) return
      setEdges((eds) => eds.map((e) => (e.id === selectedEdge.id ? { ...e, sourceHandle: port } : e)))
      setSelectedEdge({ ...selectedEdge, sourceHandle: port })
    },
    [selectedEdge, setEdges]
  )

  const deleteEdge = useCallback(() => {
    if (!selectedEdge) return
    setEdges((eds) => eds.filter((e) => e.id !== selectedEdge.id))
    setSelectedEdge(null)
  }, [selectedEdge, setEdges])

  // ===== wire 互转 =====
  const buildPayload = useCallback((): { name: string; description: string; graph: FlowGraphWire } => {
    const backendNodes = nodes.map((n) => ({
      id: n.id,
      type: n.data?.spec?.type || n.type || 'summarize',
      name: n.data?.name || n.data?.spec?.label || n.id,
      config: n.data?.config || {},
      position: n.position,
    }))
    const backendEdges = edges.map((e) => ({
      id: e.id,
      source: e.source,
      sourcePort: e.sourceHandle || 'next',
      target: e.target,
    }))
    return { name: flowName || '未命名流程', description: flowDesc, graph: { nodes: backendNodes, edges: backendEdges } }
  }, [nodes, edges, flowName, flowDesc])

  const onSave = useCallback(async () => {
    setSaving(true)
    try {
      const payload = buildPayload()
      if (flowIdRef.current) {
        await updateWorkflow(flowIdRef.current, payload)
        message.success('已保存')
      } else {
        const r = await createWorkflow(payload)
        const newId = r?.data?.id
        if (newId) {
          navigate(`/ai/workflows/editor?id=${encodeURIComponent(newId)}`, { replace: true })
        } else {
          navigate('/ai/workflows')
        }
        message.success('已创建流程')
      }
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '保存失败')
    } finally {
      setSaving(false)
    }
  }, [buildPayload, navigate])

  // ===== 运行态可视化 =====
  const applyRunStatus = useCallback(
    (run: any) => {
      const runNodes: any[] = run?.nodes || []
      const statusMap: Record<string, string> = {}
      const outputMap: Record<string, any> = {}
      runNodes.forEach((rn) => {
        if (rn?.node_id) {
          statusMap[rn.node_id] = rn.status || 'pending'
          outputMap[rn.node_id] = rn
        }
      })
      setNodes((nds) =>
        nds.map((n) => {
          const st = statusMap[n.id]
          if (!st) return n
          const d = outputMap[n.id]
          return {
            ...n,
            data: {
              ...n.data,
              runStatus: st,
              output: d ? parseRunOutput(d) : n.data.output,
              firedPort: d?.fired_port,
              nodeError: d?.error,
            },
          }
        })
      )
    },
    [setNodes]
  )

  const pollRun = useCallback(
    (fid: string, rid: string) => {
      stopPolling()
      pollRef.current = setInterval(async () => {
        try {
          const r = await getFlowRun(fid, rid)
          const run = r?.data?.run || r?.data
          if (!run) return
          setRunStatus(run.status || '')
          applyRunStatus(run)
          if (run.status === 'succeeded' || run.status === 'failed') {
            stopPolling()
            setRunning(false)
            return
          }
          if (run.status === 'waiting_approval') {
            const waitNode = (run.nodes || []).find((rn: any) => rn.node_type === 'wait_approval')
            setApproval({ runId: rid, output: parseRunOutput(waitNode) ?? null })
            stopPolling()
            setRunning(false)
          }
        } catch {
          stopPolling()
          setRunning(false)
        }
      }, 1500)
    },
    [applyRunStatus, stopPolling]
  )

  const onRun = useCallback(async () => {
    const fid = flowIdRef.current
    if (!fid) {
      message.warning('请先保存流程再运行')
      return
    }
    setRunning(true)
    setRunStatus('running')
    setApproval(null)
    setRunResult('')
    try {
      const r = await runWorkflow(fid, { trigger: { type: 'manual' }, message: runMessage || undefined, service: runService || undefined })
      const rid = r?.data?.run_id || r?.data?.id
      if (!rid) {
        message.error('运行失败')
        setRunning(false)
        return
      }
      setRunId(rid)
      setRunResult(JSON.stringify(r?.data, null, 2))
      setRunDrawerOpen(false)
      const r2 = await getFlowRun(fid, rid)
      const run = r2?.data?.run || r2?.data
      if (run) {
        setRunStatus(run.status || 'running')
        applyRunStatus(run)
        if (run.status === 'succeeded' || run.status === 'failed') {
          setRunning(false)
          return
        }
        if (run.status === 'waiting_approval') {
          const waitNode = (run.nodes || []).find((rn: any) => rn.node_type === 'wait_approval')
          setApproval({ runId: rid, output: parseRunOutput(waitNode) ?? null })
          setRunning(false)
          return
        }
      }
      pollRun(fid, rid)
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '运行失败')
      setRunning(false)
    }
  }, [applyRunStatus, pollRun, runMessage, runService])

  const onResolveApproval = useCallback(
    async (approved: boolean) => {
      if (!approval || !flowIdRef.current) return
      setApproval(null)
      setRunning(true)
      try {
        await resumeFlowRun(flowIdRef.current, approval.runId, approved)
        message.success(approved ? '已批准，流程继续' : '已拒绝')
        pollRun(flowIdRef.current, approval.runId)
      } catch (e: any) {
        message.error(e?.response?.data?.detail || e?.response?.data?.error || '审批提交失败')
        setRunning(false)
      }
    },
    [approval, pollRun]
  )

  const clearRunVisuals = useCallback(() => {
    stopPolling()
    setRunStatus('')
    setRunId('')
    setRunning(false)
    setApproval(null)
    setNodes((nds) =>
      nds.map((n) => {
        const { runStatus: _rs, output: _o, firedPort: _fp, nodeError: _ne, ...rest } = n.data
        return { ...n, data: rest }
      })
    )
  }, [setNodes, stopPolling])

  // ===== 配置表单（数据驱动 config_fields）=====
  const configFields = useMemo(() => {
    if (!selectedNode?.data?.spec?.config_fields) return []
    return selectedNode.data.spec.config_fields
  }, [selectedNode])

  const updateConfig = useCallback(
    (fieldName: string, value: unknown) => {
      if (!selectedNode) return
      const newConfig = { ...(selectedNode.data.config || {}), [fieldName]: value }
      setSelectedNode({ ...selectedNode, data: { ...selectedNode.data, config: newConfig } })
      setNodes((nds) => nds.map((n) => (n.id === selectedNode.id ? { ...n, data: { ...n.data, config: newConfig } } : n)))
    },
    [selectedNode, setNodes]
  )

  const updateNodeName = useCallback(
    (name: string) => {
      if (!selectedNode) return
      setSelectedNode({ ...selectedNode, data: { ...selectedNode.data, name } })
      setNodes((nds) => nds.map((n) => (n.id === selectedNode.id ? { ...n, data: { ...n.data, name } } : n)))
    },
    [selectedNode, setNodes]
  )

  const renderField = (f: NodeConfigField) => {
    const val = selectedNode?.data?.config?.[f.name]
    if (f.type === 'textarea') {
      return (
        <Input.TextArea rows={4} value={(val as string) || ''} onChange={(e) => updateConfig(f.name, e.target.value)} />
      )
    }
    if (f.type === 'number') {
      return (
        <InputNumber style={{ width: '100%' }} value={(val as number) ?? undefined} onChange={(v) => updateConfig(f.name, v)} />
      )
    }
    if (f.type === 'select') {
      return (
        <Select
          style={{ width: '100%' }}
          value={(val as string) || undefined}
          onChange={(v) => updateConfig(f.name, v)}
          options={(f.options || []).map((o: any) =>
            typeof o === 'string' ? { label: o, value: o } : { label: o.label, value: o.value }
          )}
          allowClear
        />
      )
    }
    // text 或其他
    return <Input value={(val as string) || ''} onChange={(e) => updateConfig(f.name, e.target.value)} />
  }

  const runTestNode = async () => {
    if (!selectedNode?.data?.spec) return
    setTestLoading(true)
    setTestResult('')
    try {
      const r = await testFlowNode({
        type: selectedNode.data.spec.type,
        config: selectedNode.data.config || {},
        trigger: { type: 'manual' },
      })
      setTestResult(JSON.stringify(r?.data ?? r, null, 2))
    } catch (e: any) {
      setTestResult(`试跑失败: ${e?.response?.data?.detail || e?.response?.data?.error || e?.message || ''}`)
    } finally {
      setTestLoading(false)
    }
  }

  // ===== NL 生成 =====
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

  const applyGenerated = () => {
    const g = genPreview?.graph
    if (!g) return
    setFlowName(genPreview?.name || '生成工作流')
    setFlowDesc(genPreview?.description || '')
    const fnodes: FlowNode[] = (g.nodes || []).map((n: any) => ({
      id: n.id,
      type: 'flow',
      position: n.position || { x: 120, y: 80 },
      data: { spec: nodeTypesMap[n.type], config: n.config || {}, name: n.name || n.type },
    }))
    const fedges: Edge[] = (g.edges || []).map((e: any) => ({
      id: e.id,
      source: e.source,
      sourceHandle: e.sourcePort || 'next',
      target: e.target,
      targetHandle: 'in',
    }))
    setNodes(fnodes)
    setEdges(fedges)
    message.success('已生成并载入画布，请检查后保存')
    setGenOpen(false)
    setGenPrompt('')
    setGenPreview(null)
  }

  const runStatusTag = () => {
    if (!runStatus) return null
    const color = runStatus === 'succeeded' ? 'green' : runStatus === 'failed' ? 'red' : runStatus === 'waiting_approval' ? 'orange' : runStatus === 'running' ? 'blue' : 'default'
    return <Tag color={color}>{runStatusText[runStatus] || runStatus}</Tag>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 150px)' }}>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '工作流', href: '/ai/workflows' }, { t: '编辑器' }]} />
      <PageHeader title="工作流编辑器" desc={flowDesc || '拖拽节点编排工作流，配置触发器 / 分析 / 审批 / 执行节点'}
        actions={
          <Space wrap>
            {runStatusTag()}
            <Button icon={<AppIcon name="sparkles" />} onClick={() => { setGenOpen(true); setGenPreview(null); setGenPrompt('') }}>NL 生成</Button>
            <Button icon={<AppIcon name="audit" />} onClick={() => setRunsOpen(true)} disabled={!flowId}>运行记录</Button>
            <Button onClick={clearRunVisuals}>清除运行状态</Button>
            <Button type="primary" loading={saving} onClick={onSave}>保存</Button>
            <Button type="primary" danger icon={<AppIcon name="send" />} onClick={() => setRunDrawerOpen(true)}>运行</Button>
          </Space>
        } />

      <div style={{ display: 'flex', flex: 1, gap: 12, minHeight: 0 }}>
        {/* 左侧节点面板 */}
        <div
          style={{
            width: 230, flexShrink: 0, background: 'var(--surface-1)', border: '1px solid var(--border)',
            borderRadius: 12, padding: 12, overflowY: 'auto',
          }}
        >
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--text)' }}>节点面板</div>
          {nodeTypesList.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无节点类型" />}
          {Object.entries(paletteGroups).map(([kind, items]) => (
            <div key={kind} style={{ marginBottom: 12 }}>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 6, display: 'flex', alignItems: 'center', gap: 6, letterSpacing: 0.5 }}>
                <span style={{ width: 8, height: 8, borderRadius: '50%', background: (KIND_STYLE[kind] || KIND_STYLE.action).dot }} />
                {kind}
              </div>
              {items.map((nt) => (
                <div
                  key={nt.type}
                  onClick={() => addNode(nt)}
                  style={{
                    padding: '8px 10px', marginBottom: 6, background: 'var(--surface-2)',
                    border: `1px solid ${(KIND_STYLE[nt.kind] || KIND_STYLE.action).border}55`, borderRadius: 8, cursor: 'grab',
                    fontSize: 12, color: 'var(--text)', display: 'flex', alignItems: 'center', gap: 8,
                  }}
                >
                  <span style={{ width: 8, height: 8, borderRadius: '50%', background: (KIND_STYLE[nt.kind] || KIND_STYLE.action).dot, flexShrink: 0 }} />
                  <span>{nt.label || nt.type}</span>
                </div>
              ))}
            </div>
          ))}
        </div>

        {/* 画布 */}
        <div style={{ flex: 1, background: 'var(--surface-1)', border: '1px solid var(--border)', borderRadius: 12, position: 'relative', minWidth: 0 }}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={onNodeClick}
            onEdgeClick={onEdgeClick}
            onPaneClick={onPaneClick}
            nodeTypes={nodeTypes}
            fitView
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={20} color="#e5e9f0" />
            <Controls style={{ background: 'var(--surface-1)', border: '1px solid var(--border)', borderRadius: 8 }} />
            <MiniMap
              nodeColor={(n) => {
                const d = n.data as any
                return d?.runStatus ? RUN_STYLE[d.runStatus]?.border || 'var(--primary)' : (KIND_STYLE[d?.spec?.kind] || KIND_STYLE.action).border
              }}
              maskColor="rgba(0,0,0,0.04)"
              style={{ width: 180, height: 120, background: 'var(--surface-1)', border: '1px solid var(--border)', borderRadius: 8 }}
            />
          </ReactFlow>
          {nodes.length === 0 && (
            <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', color: 'var(--text-muted)', fontSize: 14 }}>
              点击左侧面板节点添加节点，拖拽连线（sourcePort 可在边上配置）
            </div>
          )}
        </div>
      </div>

      {/* ===== 节点配置 Drawer ===== */}
      <Drawer
        title={selectedNode ? `配置节点：${selectedNode.data?.name || selectedNode.data?.spec?.label || ''}` : '节点配置'}
        open={!!selectedNode}
        onClose={() => { setSelectedNode(null); setTestResult('') }}
        width={420}
        extra={
          selectedNode ? (
            <Space>
              <Button size="small" icon={<AppIcon name="send" />} loading={testLoading} onClick={runTestNode}>单节点试跑</Button>
              <Popconfirm title="确定删除选中节点？" description="删除后不可撤销" okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={deleteSelected}>
                <Button size="small" danger>删除节点</Button>
              </Popconfirm>
            </Space>
          ) : null
        }
      >
        {selectedNode && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>节点名称</Text>
              <Input value={selectedNode.data?.name || ''} onChange={(e) => updateNodeName(e.target.value)} style={{ marginTop: 4 }} />
            </div>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>类型</Text>
              <div style={{ marginTop: 4 }}>
                <Tag color="blue">{selectedNode.data?.spec?.type}</Tag>
                <Tag>{selectedNode.data?.spec?.kind}</Tag>
              </div>
            </div>
            {configFields.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该节点无配置项" />}
            {configFields.map((f) => (
              <div key={f.name}>
                <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>
                  {f.label || f.name}
                  {f.required && <span style={{ color: 'var(--danger)' }}> *</span>}
                </Text>
                <div style={{ marginTop: 4 }}>{renderField(f)}</div>
              </div>
            ))}
            {testResult && (
              <div>
                <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>试跑结果</Text>
                <pre style={{ background: 'var(--surface-2)', padding: 10, borderRadius: 8, fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 240, overflow: 'auto', marginTop: 4 }}>
                  {testResult}
                </pre>
              </div>
            )}
          </div>
        )}
      </Drawer>

      {/* ===== 边配置 Drawer（sourcePort 下拉）===== */}
      <Drawer
        title="配置连线"
        open={!!selectedEdge}
        onClose={() => setSelectedEdge(null)}
        width={380}
        extra={selectedEdge ? <Button size="small" danger onClick={deleteEdge}>删除连线</Button> : null}
      >
        {selectedEdge && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>源端口 (sourcePort)</Text>
              <Select
                style={{ width: '100%', marginTop: 4 }}
                value={selectedEdge.sourceHandle || 'next'}
                onChange={(v) => updateEdgePort(v)}
                options={EDGE_PORT_OPTIONS.map((p) => ({ label: p, value: p }))}
              />
              <div style={{ marginTop: 6, fontSize: 11, color: 'var(--text-muted)' }}>
                next / true / false / approved / rejected / error —— 对应条件分支与审批节点输出端口
              </div>
            </div>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>源节点</Text>
              <div style={{ marginTop: 4 }}>
                <Tag>{nodes.find((n) => n.id === selectedEdge.source)?.data?.name || selectedEdge.source}</Tag>
              </div>
            </div>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>目标节点</Text>
              <div style={{ marginTop: 4 }}>
                <Tag>{nodes.find((n) => n.id === selectedEdge.target)?.data?.name || selectedEdge.target}</Tag>
              </div>
            </div>
          </div>
        )}
      </Drawer>

      {/* ===== 运行 Drawer ===== */}
      <Drawer title={`运行：${flowName}`} open={runDrawerOpen} onClose={() => setRunDrawerOpen(false)} width={540}
        styles={{ body: { padding: 16 } }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>服务（可选）</Text>
            <Input style={{ marginTop: 4 }} value={runService} onChange={(e) => setRunService(e.target.value)} placeholder="如 deepflow-server" />
          </div>
          <div>
            <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>触发消息（可选）</Text>
            <Input.TextArea style={{ marginTop: 4 }} rows={2} value={runMessage} onChange={(e) => setRunMessage(e.target.value)} />
          </div>
          <Button type="primary" icon={<AppIcon name="send" />} loading={running} onClick={onRun}>执行手动运行</Button>
          {runResult && (
            <pre style={{ background: 'var(--surface-2)', padding: 12, borderRadius: 8, fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 260, overflow: 'auto' }}>
              {runResult}
            </pre>
          )}
        </div>
      </Drawer>

      {/* ===== 审批 Modal ===== */}
      <Modal
        title="等待审批"
        open={!!approval}
        onCancel={() => setApproval(null)}
        footer={
          <Space>
            <Button danger loading={running} onClick={() => onResolveApproval(false)}>拒绝</Button>
            <Button type="primary" loading={running} onClick={() => onResolveApproval(true)}>批准</Button>
          </Space>
        }
      >
        {approval?.output ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>风险分</Text>
              <Tag color={(approval.output.risk_score ?? 0) >= 6 ? 'red' : (approval.output.risk_score ?? 0) >= 3 ? 'orange' : 'green'}>
                {approval.output.risk_score ?? 0}
              </Tag>
              {approval.output.risk_reason && (
                <span style={{ color: 'var(--text)', fontSize: 12 }}>{approval.output.risk_reason}</span>
              )}
            </div>
            {approval.output.plan && (
              <div>
                <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>方案</Text>
                <pre style={{ background: 'var(--surface-2)', padding: 10, borderRadius: 8, fontSize: 12, whiteSpace: 'pre-wrap', marginTop: 4 }}>{approval.output.plan}</pre>
              </div>
            )}
            {approval.output.script && (
              <div>
                <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>脚本</Text>
                <pre style={{ background: 'var(--surface-2)', padding: 10, borderRadius: 8, fontSize: 12, whiteSpace: 'pre-wrap', marginTop: 4 }}>{approval.output.script}</pre>
              </div>
            )}
          </div>
        ) : (
          <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>（无审批输出内容）</div>
        )}
      </Modal>

      {/* ===== 运行记录 Drawer ===== */}
      <Drawer title="运行记录" open={runsOpen} onClose={() => setRunsOpen(false)} width={520}>
        {runs.length === 0 && <Empty description="暂无运行记录" />}
        {runs.map((r: any) => (
          <div key={r.run_id || r.id} style={{ padding: '10px 0', borderBottom: '1px solid var(--border)' }}>
            <Space style={{ width: '100%', justifyContent: 'space-between' }}>
              <span style={{ fontSize: 12, color: 'var(--text)' }}>{r.run_id || r.id}</span>
              <Tag color={r.status === 'succeeded' ? 'green' : r.status === 'failed' ? 'red' : r.status === 'waiting_approval' ? 'orange' : 'blue'}>
                {runStatusText[r.status] || r.status}
              </Tag>
            </Space>
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              {r.trigger_type || 'manual'} · {r.created_at || ''}
            </div>
          </div>
        ))}
      </Drawer>

      {/* ===== NL 生成 Modal ===== */}
      <Modal title="NL 生成工作流" open={genOpen} onCancel={() => setGenOpen(false)}
        footer={genPreview ? (
          <Space>
            <Button onClick={() => setGenPreview(null)}>重新生成</Button>
            <Button type="primary" onClick={applyGenerated}>应用并检查</Button>
          </Space>
        ) : (
          <Button type="primary" loading={genLoading} onClick={doGenerate}>生成</Button>
        )}>
        {!genPreview ? (
          <div style={{ marginTop: 12 }}>
            <Input.TextArea rows={5} value={genPrompt} onChange={(e) => setGenPrompt(e.target.value)}
              placeholder={'用一句话描述工作流，如：\n"每小时采集 deepflow-server 指标，做 RCA 根因分析，若风险分超过 3 则进入人工审批，审批通过后执行重启，最后生成报告"'} />
            <div style={{ marginTop: 8, fontSize: 12, color: 'var(--text-muted)' }}>由 LLM 生成节点与连线，应用后可在画布上校验调整再保存。</div>
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

export default WorkflowEditor
