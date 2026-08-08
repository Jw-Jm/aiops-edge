import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, message } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { listCatalog, createCatalog, updateCatalog, deleteCatalog, CatalogItem } from '../../api/client'

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  active: { color: 'green', label: '运行中' },
  maintenance: { color: 'orange', label: '维护中' },
  deprecated: { color: 'default', label: '已弃用' },
}

const Catalog: React.FC = () => {
  const [items, setItems] = useState<CatalogItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<CatalogItem | null>(null)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listCatalog({ page: 1, size: 200 })
      setItems(r.data?.services || [])
      setTotal(r.data?.total || 0)
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const openAdd = () => { setEditing(null); form.resetFields(); setModalOpen(true) }
  const openEdit = (c: CatalogItem) => {
    setEditing(c)
    form.setFieldsValue(c)
    setModalOpen(true)
  }

  const handleSave = async () => {
    const v = await form.validateFields()
    try {
      if (editing) {
        await updateCatalog(editing.id, v)
        message.success('已更新')
      } else {
        await createCatalog(v)
        message.success('已创建')
      }
      setModalOpen(false); fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '保存失败') }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteCatalog(id)
      message.success('已删除'); fetch()
    } catch { message.error('删除失败') }
  }

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 70 },
    { title: '服务名', dataIndex: 'service_name', key: 'service_name', width: 150 },
    { title: '显示名', dataIndex: 'display_name', key: 'display_name', width: 130 },
    { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
    { title: '负责人', dataIndex: 'owner', key: 'owner', width: 110 },
    { title: '团队', dataIndex: 'team', key: 'team', width: 100 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => STATUS_MAP[v] ? <Tag color={STATUS_MAP[v].color}>{STATUS_MAP[v].label}</Tag> : (v || '-') },
    { title: '标签', dataIndex: 'tags', key: 'tags', width: 130,
      render: (v: string) => v ? v.split(',').filter(Boolean).map((t, i) => <Tag key={i} color="cyan">{t}</Tag>) : '-' },
    { title: '操作', key: 'action', width: 130, fixed: 'right' as const,
      render: (_: unknown, r: CatalogItem) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>编辑</Button>
          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
        </Space>
      ) },
  ]

  return (
    <Card title={`服务目录（${total}）`} extra={<Space>
      <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>新增服务</Button>
    </Space>}>
      <Table rowKey="id" columns={columns} dataSource={items} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />

      <Modal title={editing ? '编辑服务' : '新增服务'} open={modalOpen} onOk={handleSave} onCancel={() => setModalOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" initialValues={{ status: 'active' }}>
          <Form.Item name="service_name" label="服务名" rules={[{ required: true, message: '请输入服务名' }]}>
            <Input disabled={!!editing} placeholder="与观测数据 service_name 一致" />
          </Form.Item>
          <Form.Item name="display_name" label="显示名">
            <Input placeholder="显示名称" />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} placeholder="服务职责说明" />
          </Form.Item>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="owner" label="负责人" style={{ width: 200 }}><Input /></Form.Item>
            <Form.Item name="team" label="团队" style={{ width: 200 }}><Input /></Form.Item>
          </Space>
          <Form.Item name="status" label="状态">
            <Select options={Object.entries(STATUS_MAP).map(([k, v]) => ({ value: k, label: v.label }))} />
          </Form.Item>
          <Form.Item name="tags" label="标签（逗号分隔）">
            <Input placeholder="web,gateway" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default Catalog
