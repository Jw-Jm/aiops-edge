import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Input, Button, Modal, Form, message, Typography, Select } from 'antd'
import { PlusOutlined, SearchOutlined, ReloadOutlined, DeleteOutlined, CodeOutlined } from '@ant-design/icons'
import api from '../../api/client'

const { Text } = Typography

interface KnowledgeItem {
  id: number; title: string; content: string; source: string; tags: string
  code_ref: any; created_at: string
}

const SOURCE_MAP: Record<string, { color: string; label: string }> = {
  manual:     { color: 'blue', label: '手动' },
  code_index: { color: 'purple', label: '代码索引' },
  rag:        { color: 'geekblue', label: 'RAG' },
}

const Knowledge: React.FC = () => {
  const [items, setItems] = useState<KnowledgeItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string | number> = { page, size: 20 }
      if (q) params.q = q
      const r = await api.get('/ai/knowledge', { params })
      setItems(r.data?.items || [])
      setTotal(r.data?.total || 0)
    } catch { /* ignore */ }
    setLoading(false)
  }, [page, q])

  useEffect(() => { fetch() }, [fetch])

  const handleAdd = async () => {
    const v = await form.validateFields()
    try {
      await api.post('/ai/knowledge', v)
      message.success('已添加')
      setModalOpen(false)
      form.resetFields()
      fetch()
    } catch (e: any) {
      message.error(e?.response?.data?.detail || '添加失败')
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await api.delete(`/ai/knowledge/${id}`)
      message.success('已删除')
      fetch()
    } catch { message.error('删除失败') }
  }

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 70 },
    { title: '标题', dataIndex: 'title', key: 'title', width: 220, ellipsis: true },
    { title: '来源', dataIndex: 'source', key: 'source', width: 110,
      render: (v: string) => SOURCE_MAP[v] ? <Tag color={SOURCE_MAP[v].color}>{SOURCE_MAP[v].label}</Tag> : (v || '-') },
    { title: '内容', dataIndex: 'content', key: 'content', ellipsis: true },
    { title: '标签', dataIndex: 'tags', key: 'tags', width: 160,
      render: (v: string) => v ? v.split(',').filter(Boolean).map((t, i) => <Tag key={i} color="cyan">{t}</Tag>) : '-' },
    { title: '代码', dataIndex: 'code_ref', key: 'code_ref', width: 90,
      render: (v: any) => v ? <Tag icon={<CodeOutlined />} color="purple">索引</Tag> : '-' },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', width: 180 },
    { title: '操作', key: 'action', width: 80, fixed: 'right' as const,
      render: (_: unknown, r: KnowledgeItem) => (
        <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
      ) },
  ]

  return (
    <Card
      title="知识库"
      extra={<Space>
        <Input prefix={<SearchOutlined />} placeholder="搜索标题/内容/标签" allowClear style={{ width: 240 }}
          onPressEnter={(e: any) => { setPage(1); setQ(e.target.value.trim()) }} />
        <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>新增知识</Button>
      </Space>}
    >
      <Table rowKey="id" columns={columns} dataSource={items} loading={loading}
        pagination={{ current: page, pageSize: 20, total, onChange: setPage, showTotal: (t: number) => `共 ${t} 条` }}
      />
      <Modal title="新增知识条目" open={modalOpen} onOk={handleAdd} onCancel={() => setModalOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" initialValues={{ source: 'manual' }}>
          <Form.Item name="title" label="标题" rules={[{ required: true, message: '请输入标题' }]}>
            <Input placeholder="条目标题" />
          </Form.Item>
          <Form.Item name="content" label="内容" rules={[{ required: true, message: '请输入内容' }]}>
            <Input.TextArea rows={4} placeholder="知识内容" />
          </Form.Item>
          <Form.Item name="source" label="来源">
            <Select options={[
              { value: 'manual', label: '手动' },
              { value: 'rag', label: 'RAG' },
            ]} />
          </Form.Item>
          <Form.Item name="tags" label="标签（逗号分隔）">
            <Input placeholder="cpu,排查,命令" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default Knowledge
