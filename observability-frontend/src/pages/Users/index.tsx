import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, Switch, message, Typography, Popconfirm } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { listUsers, createUser, updateUser, deleteUser, UserItem } from '../../api/client'

const { Text } = Typography

const Users: React.FC = () => {
  const [users, setUsers] = useState<UserItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<UserItem | null>(null)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listUsers({ page: 1, size: 100 })
      setUsers(r.data?.users || [])
      setTotal(r.data?.total || 0)
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const openAdd = () => { setEditing(null); form.resetFields(); setModalOpen(true) }
  const openEdit = (u: UserItem) => {
    setEditing(u)
    form.setFieldsValue({ username: u.username, display_name: u.display_name, role: u.role, email: u.email, status: u.status === 1 })
    setModalOpen(true)
  }

  const handleSave = async () => {
    const v = await form.validateFields()
    try {
      if (editing) {
        await updateUser(editing.id, {
          display_name: v.display_name, role: v.role, email: v.email,
          status: v.status ? 1 : 0, password: v.password || '',
        })
        message.success('已更新')
      } else {
        await createUser({ ...v, status: v.status ? 1 : 0 })
        message.success('已创建')
      }
      setModalOpen(false); fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '保存失败') }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteUser(id)
      message.success('已删除'); fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '删除失败') }
  }

  const handleToggle = async (u: UserItem, enabled: boolean) => {
    try {
      await updateUser(u.id, { display_name: u.display_name, role: u.role, email: u.email, status: enabled ? 1 : 0 })
      fetch()
    } catch { message.error('切换失败') }
  }

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 70 },
    { title: '用户名', dataIndex: 'username', key: 'username', width: 140 },
    { title: '显示名', dataIndex: 'display_name', key: 'display_name', width: 140 },
    { title: '角色', dataIndex: 'role', key: 'role', width: 100,
      render: (v: string) => v === 'admin' ? <Tag color="gold">管理员</Tag> : <Tag color="blue">普通用户</Tag> },
    { title: '邮箱', dataIndex: 'email', key: 'email', ellipsis: true },
    { title: '状态', dataIndex: 'status', key: 'status', width: 90,
      render: (v: number, r: UserItem) => (
        <Switch size="small" checked={v === 1} onChange={c => handleToggle(r, c)}
          disabled={r.username === 'admin'} />
      ) },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 170 },
    { title: '操作', key: 'action', width: 130, fixed: 'right' as const,
      render: (_: unknown, r: UserItem) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>编辑</Button>
          {r.username !== 'admin' && (
            <Popconfirm title={`确定删除用户「${r.username}」？`} description="删除后该用户将无法登录，不可恢复" onConfirm={() => handleDelete(r.id)} okText="删除" cancelText="取消" okButtonProps={{ danger: true }}>
              <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
            </Popconfirm>
          )}
        </Space>
      ) },
  ]

  return (
    <Card
      title={`用户管理（${total}）`}
      extra={<Space>
        <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
        <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>新增用户</Button>
      </Space>}
    >
      <Table rowKey="id" columns={columns} dataSource={users} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />

      <Modal title={editing ? '编辑用户' : '新增用户'} open={modalOpen} onOk={handleSave} onCancel={() => setModalOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" initialValues={{ role: 'user', status: true }}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input disabled={!!editing} placeholder="登录名" />
          </Form.Item>
          <Form.Item name="display_name" label="显示名">
            <Input placeholder="显示名称" />
          </Form.Item>
          <Form.Item name="password" label={editing ? '新密码（留空不改）' : '密码'} rules={editing ? [] : [{ required: true, message: '请输入密码' }]}>
            <Input.Password placeholder={editing ? '留空则保持不变' : '初始密码'} />
          </Form.Item>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="role" label="角色" style={{ width: 180 }}>
              <Select options={[{ value: 'admin', label: '管理员' }, { value: 'user', label: '普通用户' }]} />
            </Form.Item>
            <Form.Item name="status" label="启用" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item name="email" label="邮箱">
            <Input placeholder="邮箱" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default Users
