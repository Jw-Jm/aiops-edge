import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, Switch, message, Popconfirm } from 'antd'
import { listUsers, createUser, updateUser, deleteUser } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

const AdminUsers: React.FC = () => {
  const [data, setData] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [form] = Form.useForm()

  const load = () => {
    setLoading(true)
    listUsers().then((r) => setData(Array.isArray(r.data) ? r.data : r.data?.users || r.data?.data || [])).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  const openCreate = () => { setEditing(null); form.resetFields(); setOpen(true) }
  const openEdit = (u: any) => { setEditing(u); form.setFieldsValue({ username: u.username, role: u.role, display_name: u.display_name, email: u.email }); setOpen(true) }

  const submit = async () => {
    try {
      const v = await form.validateFields()
      const req = editing ? updateUser(editing.id, v) : createUser(v)
      await req
      message.success(editing ? '已更新' : '已创建')
      setOpen(false)
      load()
    } catch (e: any) {
      // validateFields 校验失败或请求失败都会到这里；校验失败时不弹错误提示
      if (!(e && e.errorFields)) {
        message.error(e?.response?.data?.error || '操作失败')
      }
    }
  }

  const cols = [
    { title: '用户名', dataIndex: 'username', key: 'username' },
    { title: '显示名', dataIndex: 'display_name', key: 'display_name', render: (v: string, r: any) => (v && v !== r.username ? v : '-') },
    { title: '角色', dataIndex: 'role', key: 'role', render: (v: string) => <StatusBadge text={v === 'admin' ? '系统管理员' : '普通成员'} tone={v === 'admin' ? 'crit' : 'info'} /> },
    { title: '邮箱', dataIndex: 'email', key: 'email', render: (v: string) => v || '-' },
    { title: '状态', dataIndex: 'status', key: 'status', render: (v: number) => <StatusBadge text={v === 1 ? '启用' : '停用'} tone={v === 1 ? 'ok' : 'muted'} /> },
    { title: '操作', key: 'op', width: 120, render: (_: any, r: any) => (
        <span>
          <Button size="small" type="link" onClick={() => openEdit(r)}>编辑</Button>
          <Popconfirm title="确认删除该用户？" onConfirm={() => deleteUser(r.id).then(() => { message.success('已删除'); load() }).catch(() => message.error('删除失败'))}>
            <Button size="small" type="link" danger>删除</Button>
          </Popconfirm>
        </span>
      ) },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '系统管理' }, { t: '用户管理' }]} />
      <PageHeader title="用户管理" desc="平台用户、角色与访问控制"
        actions={<Button type="primary" onClick={openCreate}>新增用户</Button>} />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={false} locale={{ emptyText: <Empty text="暂无用户" /> }} />
      </div>

      <Modal title={editing ? '编辑用户' : '新增用户'} open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="username" label="用户名" rules={[{ required: true }]}><Input disabled={!!editing} /></Form.Item>
          {!editing && <Form.Item name="password" label="密码" rules={[{ required: true }]}><Input.Password /></Form.Item>}
          <Form.Item name="display_name" label="显示名"><Input /></Form.Item>
          <Form.Item name="role" label="角色" initialValue="user"><Select options={[{ value: 'user', label: '普通用户' }, { value: 'admin', label: '管理员' }]} /></Form.Item>
          <Form.Item name="email" label="邮箱" rules={[{ required: true, message: '请输入邮箱（用于密码找回）' }]}><Input /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default AdminUsers
