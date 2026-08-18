import React, { useEffect, useMemo, useState } from 'react'
import { Table, Button, Modal, Form, Input, InputNumber, Select, Switch, message, Tag, Popconfirm } from 'antd'
import { listSLOs, createSLO, updateSLO, deleteSLO, SLOTarget } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const SLO: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)
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

  // B9: 集群过滤——/slo 为全局端点，后端不按集群过滤，前端按 cluster_id 客户端过滤
  // （数字集群 id → 后端字符串 cluster_id 映射，与图谱 A5 一致）
  const clusterIdMap = useMemo(() => {
    const m = new Map<string, string>()
    clusters.forEach((c) => m.set(String(c.id), c.name))
    return m
  }, [clusters])
  const filteredData = useMemo(() => {
    if (currentClusterId === 'all') return data
    const target = clusterIdMap.get(String(currentClusterId)) || currentClusterId
    return data.filter((r: any) => {
      // 无 cluster_id 字段的 SLO 视为全局，不过滤
      if (r.cluster_id === undefined || r.cluster_id === null || r.cluster_id === '') return true
      return String(r.cluster_id) === String(target)
    })
  }, [data, currentClusterId, clusterIdMap])

  const submit = async () => {
    let v: any
    try {
      v = await form.validateFields()
    } catch {
      return // 校验失败，表单已展示错误
    }
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
          {/* B9: 删除二次确认 */}
          <Popconfirm title="确认删除该 SLO？" description="删除后不可恢复" okText="删除" cancelText="取消" okButtonProps={{ danger: true }}
            onConfirm={() => deleteSLO(r.id).then(() => { message.success('已删除'); load() }).catch(() => message.error('删除失败'))}>
            <Button size="small" type="link" danger>删除</Button>
          </Popconfirm>
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
        <Table rowKey="id" loading={loading} columns={cols} dataSource={filteredData} size="middle"
          pagination={{ pageSize: 10, showSizeChanger: false }} locale={{ emptyText: <Empty text="暂无 SLO 目标" /> }} />
      </div>
      <Modal title={editing ? '编辑 SLO' : '新建 SLO'} open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="service" label="服务" rules={[{ required: true }]}><Input placeholder="如：payments" /></Form.Item>
          <Form.Item name="slo_type" label="类型" initialValue="availability">
            <Select options={[{ value: 'availability', label: '可用性(%)' }, { value: 'latency', label: '延迟(ms)' }]} />
          </Form.Item>
          {/* B9: 目标值范围校验——availability 0-100，latency >0（随类型动态切换） */}
          <Form.Item name="target" label="目标值" dependencies={['slo_type']}
            rules={[
              { required: true, message: '请输入目标值' },
              ({ getFieldValue }) => ({
                validator: (_, value) => {
                  if (value === undefined || value === null || value === '') return Promise.resolve()
                  const t = getFieldValue('slo_type') || 'availability'
                  if (t === 'availability') {
                    if (value < 0 || value > 100) return Promise.reject(new Error('可用性目标需在 0-100 之间'))
                  } else if (value <= 0) {
                    return Promise.reject(new Error('延迟目标必须大于 0'))
                  }
                  return Promise.resolve()
                },
              }),
            ]}>
            <InputNumber style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="window_seconds" label="窗口(天)" initialValue={30}><InputNumber style={{ width: '100%' }} min={1} /></Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked" initialValue={true}><Switch /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default SLO
