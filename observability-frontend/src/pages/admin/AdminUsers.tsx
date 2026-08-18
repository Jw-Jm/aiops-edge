import React, { useEffect, useMemo, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, Switch, message, Popconfirm } from 'antd'
import { listUsers, createUser, updateUser, deleteUser } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'

// B12/C5: 角色词汇统一映射（admin/approver/user → 中文），列表与表单共用
const ROLE_LABELS: Record<string, string> = {
  admin: '系统管理员',
  approver: '审批人',
  user: '普通用户',
}
const ROLE_OPTIONS = Object.entries(ROLE_LABELS).map(([value, label]) => ({ value, label }))

const AdminUsers: React.FC = () => {
  const [data, setData] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [submitting, setSubmitting] = useState(false)
  const [search, setSearch] = useState('')
  const [form] = Form.useForm()

  const load = () => {
    setLoading(true)
    listUsers().then((r) => setData(Array.isArray(r.data) ? r.data : r.data?.users || r.data?.data || [])).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  // B12: 客户端搜索（用户名/显示名/邮箱）
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return data
    return data.filter((u) =>
      String(u.username || '').toLowerCase().includes(q) ||
      String(u.display_name || '').toLowerCase().includes(q) ||
      String(u.email || '').toLowerCase().includes(q)
    )
  }, [data, search])

  const openCreate = () => { setEditing(null); form.resetFields(); setOpen(true) }
  const openEdit = (u: any) => { setEditing(u); form.setFieldsValue({ username: u.username, role: u.role, display_name: u.display_name, email: u.email }); setOpen(true) }

  const submit = async () => {
    let v: any
    try {
      v = await form.validateFields()
    } catch {
      return // 校验失败，表单已展示错误
    }
    setSubmitting(true)
    try {
      const req = editing ? updateUser(editing.id, v) : createUser(v)
      await req
      message.success(editing ? '已更新' : '已创建')
      setOpen(false)
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || '操作失败')
    } finally {
      setSubmitting(false)
    }
  }

  const cols = [
    { title: '用户名', dataIndex: 'username', key: 'username' },
    { title: '显示名', dataIndex: 'display_name', key: 'display_name', render: (v: string, r: any) => (v && v !== r.username ? v : '-') },
    { title: '角色', dataIndex: 'role', key: 'role', render: (v: string) => <StatusBadge text={ROLE_LABELS[v] || v || '—'} tone={v === 'admin' ? 'crit' : v === 'approver' ? 'warn' : 'info'} /> },
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
        <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)' }}>
          <Input allowClear placeholder="按用户名 / 显示名 / 邮箱搜索" value={search}
            onChange={(e) => setSearch(e.target.value)} style={{ width: 260 }} />
        </div>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={filtered} size="middle"
          pagination={{ pageSize: 10, showSizeChanger: false }} locale={{ emptyText: <Empty text="暂无用户" /> }} />
      </div>

      <Modal title={editing ? '编辑用户' : '新增用户'} open={open} onOk={submit} confirmLoading={submitting} onCancel={() => setOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="username" label="用户名" rules={[{ required: true }]}><Input disabled={!!editing} /></Form.Item>
          {!editing && <Form.Item name="password" label="密码" rules={[{ required: true }]}><Input.Password /></Form.Item>}
          <Form.Item name="display_name" label="显示名"><Input /></Form.Item>
          <Form.Item name="role" label="角色" initialValue="user"><Select options={ROLE_OPTIONS} /></Form.Item>
          <Form.Item name="email" label="邮箱" rules={[{ required: true, message: '请输入邮箱（用于密码找回）' }]}><Input /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default AdminUsers
