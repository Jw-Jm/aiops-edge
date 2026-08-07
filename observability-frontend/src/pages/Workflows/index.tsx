import React, { useEffect, useState } from 'react'
import { Card, Row, Col, Button, Drawer, Form, Input, message } from 'antd'
import { listFlows, runFlow } from '../../api/client'

interface Flow {
  key: string
  name: string
  description: string
  nodes: any[]
  edges: any[]
}

const Workflows: React.FC = () => {
  const [flows, setFlows] = useState<Flow[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<Flow | null>(null)
  const [runOpen, setRunOpen] = useState(false)
  const [runParams, setRunParams] = useState({ service: '', message: '' })
  const [runResult, setRunResult] = useState('')
  const [running, setRunning] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const r = await listFlows()
      setFlows(r?.data?.flows || [])
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

  const doRun = async (key: string) => {
    setRunning(true)
    setRunResult('运行中...')
    try {
      const r = await runFlow(key, runParams)
      const res = r?.data?.result
      setRunResult(typeof res === 'string' ? res : JSON.stringify(res, null, 2))
    } catch {
      setRunResult('运行失败')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div>
      <Row gutter={[16, 16]}>
        {flows.map((f) => (
          <Col span={12} key={f.key}>
            <Card
              title={f.name}
              style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}
              extra={
                <Button
                  size="small"
                  onClick={() => {
                    setDetail(f)
                    setRunParams({ service: '', message: '' })
                    setRunResult('')
                  }}
                >
                  查看/运行
                </Button>
              }
            >
              <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 8 }}>{f.description}</div>
              <div style={{ color: 'var(--text)', fontSize: 12 }}>{f.nodes?.length} 个节点 · {f.edges?.length} 条边</div>
            </Card>
          </Col>
        ))}
      </Row>

      <Drawer title={detail?.name} open={!!detail} onClose={() => setDetail(null)} width={560}>
        {/* 只读流程图：SVG 垂直节点链 */}
        <div style={{ background: 'var(--surface-2)', padding: 16, borderRadius: 8, overflowX: 'auto' }}>
          <svg width="320" height={(detail?.nodes?.length || 1) * 56} style={{ display: 'block' }}>
            {(detail?.nodes || []).map((n: any, i: number) => (
              <g key={n.id}>
                <rect x="60" y={i * 56} width="200" height="36" rx="8" fill="#27272a" stroke="#3f3f46" />
                <text x="160" y={i * 56 + 23} textAnchor="middle" fill="#f4f4f5" fontSize="12">
                  {n.label}
                </text>
                {i < (detail?.nodes?.length || 1) - 1 && <line x1="160" y1={i * 56 + 36} x2="160" y2={(i + 1) * 56} stroke="#3f3f46" />}
              </g>
            ))}
          </svg>
        </div>
        <div style={{ marginTop: 16 }}>
          <Button type="primary" onClick={() => setRunOpen(true)}>
            运行
          </Button>
        </div>
      </Drawer>

      <Drawer title={`运行 ${detail?.name}`} open={runOpen} onClose={() => setRunOpen(false)} width={520}>
        <Form layout="vertical">
          <Form.Item label="服务">
            <Input value={runParams.service} onChange={(e) => setRunParams({ ...runParams, service: e.target.value })} placeholder="如 deepflow-server" />
          </Form.Item>
          <Form.Item label="诊断诉求">
            <Input.TextArea value={runParams.message} onChange={(e) => setRunParams({ ...runParams, message: e.target.value })} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" loading={running} onClick={() => detail && doRun(detail.key)}>
              执行诊断
            </Button>
          </Form.Item>
        </Form>
        {runResult && (
          <pre
            style={{
              background: 'var(--surface-2)', padding: 12, borderRadius: 8, color: 'var(--text)',
              fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 300, overflow: 'auto',
            }}
          >
            {runResult}
          </pre>
        )}
      </Drawer>
    </div>
  )
}

export default Workflows
