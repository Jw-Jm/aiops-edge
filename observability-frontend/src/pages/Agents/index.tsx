import React, { useEffect, useState } from 'react'
import { Card, Col, Row, Tag, message, Spin } from 'antd'
import { listAgents } from '../../api/client'

const Agents: React.FC = () => {
  const [agents, setAgents] = useState<any[]>([])
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try {
      const r = await listAgents()
      setAgents(r?.data?.agents || [])
    } catch {
      message.error('加载助理失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (loading) return <Spin style={{ display: 'block', margin: '40px auto' }} />
  return (
    <div>
      <Row gutter={[16, 16]}>
        {agents.map((a) => (
          <Col span={8} key={a.name}>
            <Card title={a.name} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
              <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 8 }}>{a.role}</div>
              <div style={{ color: 'var(--text)', fontSize: 13, marginBottom: 8 }}>{a.goal}</div>
              <div>
                {(a.skills || []).map((s: string) => (
                  <Tag key={s} style={{ margin: 2 }}>
                    {s}
                  </Tag>
                ))}
              </div>
            </Card>
          </Col>
        ))}
      </Row>
    </div>
  )
}

export default Agents
