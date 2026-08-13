import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, InputNumber, Select, Switch, message, Tag } from 'antd'
import { listSLOs, createSLO, updateSLO, deleteSLO, SLOTarget } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

const SLO: React.FC = () => {
  const [data, setData] = useState<SLOTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<SLOTarget | null>(null)
  const [form] = Form.useForm()

  const load = () => {
    setLoading(true)
    listSLOs()
      .then((r) => {
        const d = Array.isArray(r.data) ? r.data : r.data?.data || r.data?.items || []
        setData(d)
      })
      .catch(() => setData([]))
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  const submit = async () => {
    const v = await form.validateFields()
    // P1-4：窗口单位为"天"，提交时转秒
    const payload = {
      ...v,
      slo_type: v.slo_type || 'availability',
      window_seconds: (v.window_seconds || 30) * 86400,
    }
    const p = editing
      ? updateSLO(editing.id, payload)
      : createSLO(payload)
    p.then(() => { message.success('已保存'); setOpen(false); load() })
      .catch((e) => message.error(e?.response?.data?.error || '保存失败'))
  }

  const cols = [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '服务', dataIndex: 'service', key: 'service' },
    { title: '类型', dataIndex: 'slo_type', key: 'slo_type', render: (v: string) => (v === 'latency' ? '延迟' : '可用性') },
    { title: '目标', dataIndex: 'target', key: 'target', render: (v: number, r: SLOTarget) => (r.slo_type === 'latency' ? `${v}ms` : `${v}%`) },
    { title: '窗口', dataIndex: 'window_seconds', key: 'window_seconds', render: (v: number) => `${Math.round((v || 0) / 86400)} 天` },
    {
      title: '状态', dataIndex: 'enabled', key: 'enabled',
      render: (v: boolean) => (v ? <Tag color="green">启用</Tag> : <Tag>禁用</Tag>),
    },
    {
      title: '操作', key: 'act',
      render: (_: unknown, r: SLOTarget) => (
        <>
          <Button size="small" type="link" onClick={() => {
            setEditing(r)
            form.setFieldsValue({ ...r, window_seconds: Math.round((r.window_seconds || 2592000) / 86400) })
            setOpen(true)
          }}>编辑</Button>
          <Button size="small" type="link" danger onClick={() => {
            deleteSLO(r.id).then(() => { message.success('已删除'); load() }).catch(() => message.error('删除失败'))
          }}>删除</Button>
        </>
      ),
    },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: 'SLO 目标' }]} />
      <PageHeader title="SLO 目标" desc="服务可用性/延迟目标，驱动烧毁率告警"
        actions={<Button type="primary" onClick={() => { setEditing(null); form.resetFields(); setOpen(true) }}>新建 SLO</Button>} />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={false} locale={{ emptyText: <Empty text="暂无 SLO 目标" /> }} />
      </div>
      <Modal title={editing ? '编辑 SLO' : '新建 SLO'} open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="service" label="服务" rules={[{ required: true }]}><Input placeholder="如：payments" /></Form.Item>
          <Form.Item name="slo_type" label="类型" initialValue="availability">
            <Select options={[{ value: 'availability', label: '可用性(%)' }, { value: 'latency', label: '延迟(ms)' }]} />
          </Form.Item>
          <Form.Item name="target" label="目标值" rules={[{ required: true }]}><InputNumber style={{ width: '100%' }} /></Form.Item>
          <Form.Item name="window_seconds" label="窗口(天)" initialValue={30}><InputNumber style={{ width: '100%' }} min={1} /></Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked" initialValue={true}><Switch /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default SLO
