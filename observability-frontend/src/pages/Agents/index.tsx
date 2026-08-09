import React, { useEffect, useState } from 'react'
import { Card, Col, Row, Tag, message, Spin, Button, Modal, Form, Input, Popconfirm } from 'antd'
import { listAgents, createAgent, updateAgent, deleteAgent } from '../../api/client'

interface AgentForm {
  name: string
  role: string
  goal: string
  backstory: string
  intent_keywords: string
  skills: string
}

const emptyForm: AgentForm = { name: '', role: '', goal: '', backstory: '', intent_keywords: '', skills: '' }

const Agents: React.FC = () => {
  const [agents, setAgents] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [form, setForm] = useState<AgentForm>(emptyForm)

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

  const openCreate = () => {
    setEditing(null)
    setForm(emptyForm)
    setModalOpen(true)
  }
  const openEdit = (a: any) => {
    setEditing(a)
    setForm({
      name: a.name || '',
      role: a.role || '',
      goal: a.goal || '',
      backstory: a.backstory || '',
      intent_keywords: (a.intent_keywords || []).join(','),
      skills: (a.skills || []).join(','),
    })
    setModalOpen(true)
  }
  const onSave = async () => {
    const payload = {
      ...form,
      intent_keywords: form.intent_keywords.split(',').map((s) => s.trim()).filter(Boolean),
      skills: form.skills.split(',').map((s) => s.trim()).filter(Boolean),
    }
    try {
      if (editing) await updateAgent(editing.name, payload)
      else await createAgent(payload)
      message.success('已保存')
      setModalOpen(false)
      load()
    } catch {
      message.error('保存失败')
    }
  }
  const onDelete = async (a: any) => {
    try {
      await deleteAgent(a.name)
      message.success('已删除')
      load()
    } catch {
      message.error('内置助理不可删除')
    }
  }

  if (loading) return <Spin style={{ display: 'block', margin: '40px auto' }} />
  return (
    <div>
      <Button type="primary" style={{ marginBottom: 16 }} onClick={openCreate}>
        新建助理
      </Button>
      <Row gutter={[16, 16]}>
        {agents.map((a) => (
          <Col span={8} key={a.name}>
            <Card
              title={a.name}
              style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}
              extra={
                <span>
                  <a onClick={() => openEdit(a)} style={{ color: '#60a5fa', marginRight: 8 }}>编辑</a>
                  <Popconfirm title={`确定删除助理「${a.name}」？`} description="删除后不可恢复" onConfirm={() => onDelete(a)} okText="删除" cancelText="取消" okButtonProps={{ danger: true }}>
                    <a style={{ color: '#ef4444' }}>删除</a>
                  </Popconfirm>
                </span>
              }
            >
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

      <Modal
        title={editing ? `编辑 ${editing.name}` : '新建助理'}
        open={modalOpen}
        onOk={onSave}
        onCancel={() => setModalOpen(false)}
        okText="保存"
        cancelText="取消"
      >
        <Form layout="vertical">
          <Form.Item label="名称（唯一）">
            <Input value={form.name} disabled={!!editing} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </Form.Item>
          <Form.Item label="角色">
            <Input value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })} />
          </Form.Item>
          <Form.Item label="目标">
            <Input.TextArea value={form.goal} onChange={(e) => setForm({ ...form, goal: e.target.value })} />
          </Form.Item>
          <Form.Item label="背景">
            <Input.TextArea value={form.backstory} onChange={(e) => setForm({ ...form, backstory: e.target.value })} />
          </Form.Item>
          <Form.Item label="意图关键词（逗号分隔）">
            <Input value={form.intent_keywords} onChange={(e) => setForm({ ...form, intent_keywords: e.target.value })} />
          </Form.Item>
          <Form.Item label="技能（逗号分隔）">
            <Input value={form.skills} onChange={(e) => setForm({ ...form, skills: e.target.value })} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Agents
