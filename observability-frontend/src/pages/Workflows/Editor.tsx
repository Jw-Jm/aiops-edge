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
  type EdgeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  Layout, Button, Drawer, Modal, Tag, Input, InputNumber, Select, Space, message, Empty, Typography, Popconfirm,
} from 'antd'
import { SaveOutlined, PlayCircleOutlined, ArrowLeftOutlined, DeleteOutlined, HistoryOutlined, PlusOutlined } from '@ant-design/icons'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  listNodeTypes, getFlow, createFlow, updateFlow, runFlowAsync, listFlowRuns, getFlowRun, resumeFlowRun,
} from '../../api/client'

const { Text } = Typography

// ===== Types =====
interface PortSpec {
  name: string
  direction: 'in' | 'out'
  kind?: string
  optional?: boolean
}

interface NodeTypeSpec {
  type: string
  kind: string
  category: string
  label: string
  ports: PortSpec[]
  config_fields: Array<{
    name: string
    label?: string
    type: string
    required?: boolean
    default?: unknown
    options?: string[] | Array<{ label: string; value: string }>
  }>
  output_shape?: unknown
}

interface FlowNodeData {
  spec?: NodeTypeSpec
  config: Record<string, unknown>
  name: string
  runStatus?: string
  output?: unknown
  firedPort?: unknown
  nodeError?: unknown
  [key: string]: unknown
}

type FlowNode = Node<FlowNodeData>

// Run status → 中文标签（降低英文状态理解门槛）
export const runStatusText: Record<string, string> = {
  succeeded: '已成功', failed: '已失败', running: '执行中', pending: '等待中',
  waiting_approval: '待审批', cancelled: '已取消',
}

// Run status → border color / node style
const STATUS_STYLE: Record<string, { border: string; bg: string; color: string }> = {
  succeeded: { border: '#52c41a', bg: 'rgba(82,196,26,0.10)', color: '#52c41a' },
  failed: { border: '#ff4d4f', bg: 'rgba(255,77,79,0.12)', color: '#ff4d4f' },
  running: { border: '#1677ff', bg: 'rgba(22,119,255,0.12)', color: '#1677ff' },
  waiting: { border: '#faad14', bg: 'rgba(250,173,20,0.14)', color: '#faad14' },
  pending: { border: '#8c8c8c', bg: 'rgba(140,140,140,0.08)', color: '#8c8c8c' },
}

// Backend stores node output as `output_json` (a JSON string); parse it safely.
function parseOutput(rn: any): any {
  const raw = rn?.output_json
  if (raw === undefined || raw === null) return undefined
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw)
    } catch {
      return { raw }
    }
  }
  return raw
}

// Output port naming for a node type. condition/wait_approval have named output ports.
function outputPorts(spec?: NodeTypeSpec): string[] {
  if (!spec) return ['next', 'error']
  const outs = (spec.ports || [])
    .filter((p) => p.direction === 'out')
    .map((p) => p.name)
  if (outs.length === 0) return ['next', 'error']
  const ports = [...outs]
  if (!ports.includes('error')) ports.push('error')
  return ports
}

// ===== Custom Node Component =====
function FlowNodeComponent({ data, selected }: NodeProps<FlowNode>) {
  const spec = data.spec
  const outs = outputPorts(spec)
  const style = data.runStatus ? STATUS_STYLE[data.runStatus] : undefined
  const label = data.name || spec?.label || 'Node'

  return (
    <div
      style={{
        minWidth: 160,
        background: style ? style.bg : '#1f1f23',
        border: `1.5px solid ${style ? style.border : selected ? '#1677ff' : '#3f3f46'}`,
        borderRadius: 10,
        padding: '10px 14px',
        boxShadow: '0 4px 14px rgba(0,0,0,0.3)',
        position: 'relative',
      }}
    >
      {/* Target handle (left) */}
      <Handle type="target" position={Position.Left} id="in" style={{ background: '#8c8c8c' }} />
      <div style={{ fontSize: 12, color: '#f4f4f5', fontWeight: 600, textAlign: 'center' }}>{label}</div>
      {data.runStatus && (
        <div style={{ fontSize: 10, color: style?.color, textAlign: 'center', marginTop: 4 }}>
          {runStatusText[data.runStatus] || data.runStatus}
        </div>
      )}
      {/* Source handles: for multi-port nodes render named handles vertically; single 'next' on right */}
      {outs.length === 2 && outs[0] === 'next' ? (
        <>
          <Handle type="source" position={Position.Right} id="next" style={{ background: '#1677ff' }} />
          <Handle type="source" position={Position.Bottom} id="error" style={{ background: '#ff4d4f', top: 'auto', bottom: -4 }} />
        </>
      ) : (
        <>
          {outs.map((p, i) => {
            const isError = p === 'error'
            return (
              <Handle
                key={p}
                type="source"
                position={Position.Right}
                id={p}
                style={{
                  background: isError ? '#ff4d4f' : '#1677ff',
                  top: `${(i + 1) * (100 / (outs.length + 1))}%`,
                }}
              />
            )
          })}
          {!outs.includes('error') && (
            <Handle type="source" position={Position.Bottom} id="error" style={{ background: '#ff4d4f', top: 'auto', bottom: -4 }} />
          )}
        </>
      )}
    </div>
  )
}

const nodeTypes: NodeTypes = { flow: FlowNodeComponent }

// ===== Main Editor =====
const WorkflowEditor: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const flowId = searchParams.get('id') || undefined

  const [nodeTypesMap, setNodeTypesMap] = useState<Record<string, NodeTypeSpec>>({})
  const [nodeTypesList, setNodeTypesList] = useState<NodeTypeSpec[]>([])
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [selectedNode, setSelectedNode] = useState<FlowNode | null>(null)
  const [flowName, setFlowName] = useState('未命名流程')
  const [flowDesc, setFlowDesc] = useState('')
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [runStatus, setRunStatus] = useState('')
  const [runId, setRunId] = useState('')
  const [runs, setRuns] = useState<any[]>([])
  const [runsOpen, setRunsOpen] = useState(false)
  const [approval, setApproval] = useState<{ runId: string; output: any } | null>(null)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const flowIdRef = useRef(flowId)
  flowIdRef.current = flowId

  // Load node types once
  useEffect(() => {
    listNodeTypes()
      .then((r) => {
        const list: NodeTypeSpec[] = r?.data?.node_types || r?.data || []
        const map: Record<string, NodeTypeSpec> = {}
        list.forEach((nt) => { if (nt?.type) map[nt.type] = nt })
        setNodeTypesList(list)
        setNodeTypesMap(map)
      })
      .catch(() => message.error('加载节点类型失败'))
  }, [])

  // Load flow if editing
  useEffect(() => {
    if (!flowId) return
    let cancelled = false
    const load = async () => {
      try {
        const r = await getFlow(flowId)
        const flow = r?.data?.flow || r?.data || {}
        if (cancelled) return
        setFlowName(flow.name || '未命名流程')
        setFlowDesc(flow.description || '')
        const g = flow.graph || {}
        const backendNodes: any[] = g.nodes || []
        const backendEdges: any[] = g.edges || []
        const fnodes: FlowNode[] = backendNodes.map((n) => ({
          id: n.id,
          type: 'flow',
          position: n.position || { x: 120, y: 80 },
          data: { spec: nodeTypesMap[n.type], config: n.config || {}, name: n.name || n.type },
        }))
        const fedges: Edge[] = (backendEdges || []).map((e) => ({
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

  // Add node to canvas (default type id = first available or passed)
  const addNode = useCallback(
    (spec: NodeTypeSpec) => {
      const id = `n${Date.now()}${Math.floor(Math.random() * 1000)}`
      const position = { x: 120 + nodes.length * 24, y: 80 + nodes.length * 24 }
      const node: FlowNode = {
        id,
        type: 'flow',
        position,
        data: { spec, config: {}, name: spec.label || spec.type },
      }
      setNodes((nds) => nds.concat(node))
    },
    [nodes.length, setNodes]
  )

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
  }, [])

  const deleteSelected = useCallback(() => {
    if (!selectedNode) return
    setNodes((nds) => nds.filter((n) => n.id !== selectedNode.id))
    setEdges((eds) => eds.filter((e) => e.source !== selectedNode.id && e.target !== selectedNode.id))
    setSelectedNode(null)
  }, [selectedNode, setNodes, setEdges])

  const buildPayload = useCallback(() => {
    const backendNodes = nodes.map((n) => ({
      id: n.id,
      type: n.data?.spec?.type || n.type,
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
        await updateFlow(flowIdRef.current, payload)
        message.success('已保存')
      } else {
        const r = await createFlow(payload)
        const newId = r?.data?.flow?.id || r?.data?.id
        if (newId) {
          navigate(`/workflows/editor?id=${encodeURIComponent(newId)}`, { replace: true })
        } else {
          navigate('/workflows')
        }
        message.success('已创建流程')
      }
    } catch {
      message.error('保存失败')
    } finally {
      setSaving(false)
    }
  }, [buildPayload, navigate])

  // Apply run node statuses to canvas
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
              output: d ? parseOutput(d) : n.data.output,
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
            // find wait_approval node output (stored in output_json as a JSON string)
            const waitNode = (run.nodes || []).find((rn: any) => rn.node_type === 'wait_approval')
            setApproval({ runId: rid, output: parseOutput(waitNode) ?? null })
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
    try {
      const r = await runFlowAsync(fid, {})
      const rid = r?.data?.run_id || r?.data?.id
      if (!rid) {
        message.error('运行失败')
        setRunning(false)
        return
      }
      setRunId(rid)
      // initial fetch
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
          setApproval({ runId: rid, output: parseOutput(waitNode) ?? null })
          setRunning(false)
          return
        }
      }
      pollRun(fid, rid)
    } catch {
      message.error('运行失败')
      setRunning(false)
    }
  }, [applyRunStatus, pollRun])

  const onResolveApproval = useCallback(
    async (approved: boolean) => {
      if (!approval || !flowIdRef.current) return
      setApproval(null)
      setRunning(true)
      try {
        await resumeFlowRun(flowIdRef.current, approval.runId, approved)
        pollRun(flowIdRef.current, approval.runId)
      } catch {
        message.error('审批提交失败')
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

  const paletteGroups = useMemo(() => {
    const groups: Record<string, NodeTypeSpec[]> = {}
    nodeTypesList.forEach((nt) => {
      const cat = nt.category || '其他'
      if (!groups[cat]) groups[cat] = []
      groups[cat].push(nt)
    })
    return groups
  }, [nodeTypesList])

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

  const fieldTypeLabel = useCallback((f: any) => f.label || f.name, [])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 150px)' }}>
      {/* Top bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12, flexWrap: 'wrap' }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')}>返回</Button>
        <Input
          value={flowName}
          onChange={(e) => setFlowName(e.target.value)}
          style={{ width: 220 }}
          placeholder="流程名称"
        />
        <Input
          value={flowDesc}
          onChange={(e) => setFlowDesc(e.target.value)}
          style={{ width: 320 }}
          placeholder="流程描述"
        />
        {runStatus && <Tag color={runStatus === 'succeeded' ? 'green' : runStatus === 'failed' ? 'red' : runStatus === 'waiting_approval' ? 'orange' : 'blue'}>{runStatusText[runStatus] || runStatus}</Tag>}
        <Space style={{ marginLeft: 'auto' }}>
          <Button icon={<HistoryOutlined />} onClick={() => setRunsOpen(true)} disabled={!flowId}>运行记录</Button>
          <Button icon={<DeleteOutlined />} onClick={clearRunVisuals}>清除运行状态</Button>
          <Button icon={<SaveOutlined />} type="primary" loading={saving} onClick={onSave}>保存</Button>
          <Button icon={<PlayCircleOutlined />} type="primary" danger loading={running} onClick={onRun}>运行</Button>
        </Space>
      </div>

      <div style={{ display: 'flex', flex: 1, gap: 12, minHeight: 0 }}>
        {/* Left palette */}
        <div
          style={{
            width: 230, flexShrink: 0, background: 'var(--surface)', border: '1px solid var(--border)',
            borderRadius: 10, padding: 12, overflowY: 'auto',
          }}
        >
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--text)' }}>节点调色板</div>
          {Object.keys(paletteGroups).length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无节点类型" />}
          {Object.entries(paletteGroups).map(([cat, items]) => (
            <div key={cat} style={{ marginBottom: 12 }}>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 6, letterSpacing: 0.5 }}>{cat}</div>
              {items.map((nt) => (
                <div
                  key={nt.type}
                  onClick={() => addNode(nt)}
                  style={{
                    padding: '8px 10px', marginBottom: 6, background: 'var(--surface-2)',
                    border: '1px solid var(--border)', borderRadius: 8, cursor: 'grab',
                    fontSize: 12, color: 'var(--text)', display: 'flex', alignItems: 'center', gap: 8,
                  }}
                >
                  <PlusOutlined style={{ color: 'var(--text-muted)' }} />
                  <span>{nt.label || nt.type}</span>
                </div>
              ))}
            </div>
          ))}
        </div>

        {/* Canvas */}
        <div style={{ flex: 1, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, position: 'relative' }}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={onNodeClick}
            nodeTypes={nodeTypes}
            fitView
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={20} color="#27272a" />
            <Controls style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8 }} />
            <MiniMap
              nodeColor={(n) => {
                const d = n.data as any
                return d?.runStatus ? STATUS_STYLE[d.runStatus]?.border || '#1677ff' : '#3f3f46'
              }}
              maskColor="rgba(0,0,0,0.7)"
              style={{ width: 180, height: 120, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8 }}
            />
          </ReactFlow>
          {nodes.length === 0 && (
            <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', color: 'var(--text-muted)', fontSize: 14 }}>
              点击左侧调色板节点添加节点，拖动连线
            </div>
          )}
        </div>
      </div>

      {/* Config drawer */}
      <Drawer
        title={selectedNode ? `配置节点: ${selectedNode.data?.name || selectedNode.data?.spec?.label}` : '节点配置'}
        open={!!selectedNode}
        onClose={() => setSelectedNode(null)}
        width={400}
        extra={
          <Space>
            <Popconfirm title="确定删除选中节点？" description="删除后不可撤销" okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={deleteSelected}>
              <Button size="small" danger icon={<DeleteOutlined />}>删除节点</Button>
            </Popconfirm>
          </Space>
        }
      >
        {selectedNode && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div>
              <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>节点名称</Text>
              <Input value={selectedNode.data?.name || ''} onChange={(e) => updateNodeName(e.target.value)} style={{ marginTop: 4 }} />
            </div>
            {configFields.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该节点无配置项" />}
            {configFields.map((f: any) => (
              <div key={f.name}>
                <Text style={{ color: 'var(--text-muted)', fontSize: 12 }}>
                  {fieldTypeLabel(f)}
                  {f.required && <span style={{ color: '#ff4d4f' }}> *</span>}
                </Text>
                <div style={{ marginTop: 4 }}>
                  {f.type === 'textarea' && (
                    <Input.TextArea
                      value={(selectedNode.data.config?.[f.name] as string) || ''}
                      onChange={(e) => updateConfig(f.name, e.target.value)}
                      rows={4}
                    />
                  )}
                  {f.type === 'number' && (
                    <InputNumber
                      style={{ width: '100%' }}
                      value={(selectedNode.data.config?.[f.name] as number) ?? undefined}
                      onChange={(v) => updateConfig(f.name, v)}
                    />
                  )}
                  {f.type === 'select' && (
                    <Select
                      style={{ width: '100%' }}
                      value={(selectedNode.data.config?.[f.name] as string) || undefined}
                      onChange={(v) => updateConfig(f.name, v)}
                      options={(f.options || []).map((o: any) =>
                        typeof o === 'string' ? { label: o, value: o } : { label: o.label, value: o.value }
                      )}
                      allowClear
                    />
                  )}
                  {(f.type === 'text' || !f.type) && (
                    <Input
                      value={(selectedNode.data.config?.[f.name] as string) || ''}
                      onChange={(e) => updateConfig(f.name, e.target.value)}
                    />
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </Drawer>

      {/* Approval modal */}
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

      {/* Run history drawer */}
      <Drawer title="运行记录" open={runsOpen} onClose={() => setRunsOpen(false)} width={520}>
        {runs.length === 0 && <Empty description="暂无运行记录" />}
        {runs.map((r: any) => (
          <div key={r.id || r.run_id} style={{ padding: '10px 0', borderBottom: '1px solid var(--border)' }}>
            <Space style={{ width: '100%', justifyContent: 'space-between' }}>
              <span style={{ fontSize: 12, color: 'var(--text)' }}>
                {r.run_id || r.id} · {runStatusText[r.status] || r.status}
              </span>
              <Tag color={r.status === 'succeeded' ? 'green' : r.status === 'failed' ? 'red' : r.status === 'waiting_approval' ? 'orange' : 'blue'}>
                {runStatusText[r.status] || r.status}
              </Tag>
            </Space>
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              {r.started_at || r.created_at || ''}
            </div>
          </div>
        ))}
      </Drawer>
    </div>
  )
}

export default WorkflowEditor
