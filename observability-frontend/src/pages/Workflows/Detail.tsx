import React, { useEffect, useState } from 'react'
import { Card, Descriptions, Table, Tag, Button, Space, Timeline, Typography, message, Drawer, Form, Input } from 'antd'
import { ArrowLeftOutlined, EditOutlined, PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { useNavigate, useParams } from 'react-router-dom'
import { getFlow, runFlow, listFlowRuns } from '../../api/client'

interface FlowDetail {
  key: string
  id?: string
  name: string
  description: string
  nodes: Array<{ id: string; type?: string; label?: string; [k: string]: any }>
  edges: Array<{ id?: string; source?: string; target?: string; [k: string]: any }>
  enabled?: boolean
}

interface FlowRun {
  run_id?: string
  status?: string
  triggered_at?: string
  result?: string
  [k: string]: any
}

const WorkflowDetail: React.FC = () => {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [flow, setFlow] = useState<FlowDetail | null>(null)
  const [runs, setRuns] = useState<FlowRun[]>([])
  const [loading, setLoading] = useState(true)
  const [runOpen, setRunOpen] = useState(false)
  const [running, setRunning] = useState(false)
  const [runResult, setRunResult] = useState('')
  const [form] = Form.useForm()

  const load = async () => {
    if (!id) return
    setLoading(true)
    try {
      const r = await getFlow(id)
      setFlow(r?.data || null)
      const rr = await listFlowRuns(id)
      setRuns(rr?.data?.runs || rr?.data?.items || [])
    } catch {
      message.error('加载工作流失败')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { load() }, [id])

  const doRun = async () => {
    if (!id) return
    setRunning(true)
    setRunResult('')
    try {
      const params = form.getFieldsValue()
      const r = await runFlow(id, params)
      setRunResult(JSON.stringify(r?.data ?? r, null, 2))
      message.success('执行成功')
    } catch (e: any) {
      message.error('执行失败')
      setRunResult(`执行失败: ${e?.message || ''}`)
    } finally {
      setRunning(false)
    }
  }

  const statusColor = (s?: string) => s === 'success' || s === 'completed' ? 'green' : s === 'running' || s === 'pending' ? 'blue' : s === 'failed' ? 'red' : 'default'

  return (
    <div style={{ padding: 4 }}>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')}>返回</Button>
        <Typography.Title level={4} style={{ margin: 0 }}>{flow?.name || '工作流详情'}</Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={load}>刷新</Button>
        {/* Editor 读取 ?id= 参数，这里用 id 保持一致，避免编辑页读不到流程 */}
        <Button icon={<EditOutlined />} onClick={() => navigate(`/workflows/editor?id=${encodeURIComponent(flow?.id || id || '')}`)}>编辑</Button>
        <Button type="primary" icon={<PlayCircleOutlined />} onClick={() => setRunOpen(true)}>执行</Button>
      </Space>

      {flow && (
        <Card size="small" style={{ marginBottom: 16 }}>
          <Descriptions column={2} size="small">
            <Descriptions.Item label="标识">{flow.key || flow.id}</Descriptions.Item>
            <Descriptions.Item label="状态">{flow.enabled ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>}</Descriptions.Item>
            <Descriptions.Item label="节点数">{flow.nodes?.length || 0}</Descriptions.Item>
            <Descriptions.Item label="边数">{flow.edges?.length || 0}</Descriptions.Item>
            <Descriptions.Item label="描述" span={2}>{flow.description || '—'}</Descriptions.Item>
          </Descriptions>
        </Card>
      )}

      <Card size="small" title="节点" style={{ marginBottom: 16 }}>
        <Table
          rowKey={(r) => r.id || String(r)}
          dataSource={flow?.nodes || []} size="small" pagination={false}
          columns={[
            { title: '节点 ID', dataIndex: 'id' },
            { title: '类型', dataIndex: 'type', render: (t) => <Tag>{t || '—'}</Tag> },
            { title: '名称', dataIndex: 'label' },
          ]}
        />
      </Card>

      <Card size="small" title="边关系" style={{ marginBottom: 16 }}>
        <Table
          rowKey={(r, i) => String(i ?? 0)}
          dataSource={flow?.edges || []} size="small" pagination={false}
          columns={[
            { title: '源', dataIndex: 'source', render: (v, r) => r.sourceHandle || v },
            { title: '→', dataIndex: 'arrow', width: 50, render: () => '→' },
            { title: '目标', dataIndex: 'target', render: (v, r) => r.targetHandle || v },
          ]}
        />
      </Card>

      <Card size="small" title="执行记录" style={{ marginBottom: 16 }}>
        {runs.length === 0 ? (
          <Typography.Text type="secondary">暂无执行记录</Typography.Text>
        ) : (
          <Timeline
            items={runs.slice(0, 20).map((run) => ({
              color: run.status === 'failed' ? 'red' : run.status === 'running' ? 'blue' : 'green',
              children: (
                <div>
                  <Space>
                    <Tag color={statusColor(run.status)}>{run.status || '—'}</Tag>
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>{run.triggered_at || run.run_id || ''}</Typography.Text>
                  </Space>
                  {run.result && <div style={{ fontSize: 12, marginTop: 4, whiteSpace: 'pre-wrap' }}>{String(run.result).slice(0, 200)}</div>}
                </div>
              ),
            }))}
          />
        )}
      </Card>

      <Drawer title={`执行 ${flow?.name || id}`} width={520} open={runOpen} onClose={() => setRunOpen(false)}
        extra={<Button type="primary" loading={running} onClick={doRun} icon={<PlayCircleOutlined />}>执行</Button>}>
        <Form form={form} layout="vertical">
          <Form.Item label="服务" name="service"><Input placeholder="目标服务（可选）" /></Form.Item>
          <Form.Item label="触发消息" name="message"><Input.TextArea rows={4} placeholder="触发消息（可选）" /></Form.Item>
        </Form>
        {runResult && (
          <div>
            <Typography.Text strong>执行结果</Typography.Text>
            <pre style={{ background: 'rgba(127,127,127,0.1)', padding: 12, borderRadius: 6, marginTop: 8, whiteSpace: 'pre-wrap', fontSize: 12 }}>{runResult}</pre>
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default WorkflowDetail
