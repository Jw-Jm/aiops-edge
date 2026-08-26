import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, Tag, Space, Empty as AntdEmpty, message } from 'antd'
import { getChanges, postChange } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

// 变更类型 → 颜色（后端返回任意字符串也能兜底显示）
const CHANGE_TYPES: { value: string; label: string; color: string }[] = [
  { value: 'deploy', label: '发布', color: 'blue' },
  { value: 'release', label: '发布', color: 'blue' },
  { value: 'upgrade', label: '升级', color: 'purple' },
  { value: 'config', label: '配置变更', color: 'geekblue' },
  { value: 'config_change', label: '配置变更', color: 'geekblue' },
  { value: 'scale_up', label: '扩容', color: 'green' },
  { value: 'expand', label: '扩容', color: 'green' },
  { value: 'scale_down', label: '缩容', color: 'orange' },
  { value: 'shrink', label: '缩容', color: 'orange' },
  { value: 'restart', label: '重启', color: 'gold' },
  { value: 'rollback', label: '回滚', color: 'red' },
  { value: 'maintenance', label: '维护', color: 'cyan' },
]

function changeTag(v?: string) {
  const t = CHANGE_TYPES.find((c) => c.value === String(v || '').toLowerCase())
  return <Tag color={t?.color ?? 'default'} style={{ marginRight: 0 }}>{(t?.label ?? v) || '未知'}</Tag>
}

function fmtTime(v?: string): string {
  if (!v) return '-'
  return String(v).replace('T', ' ').slice(0, 19)
}

const Changes: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)

  const [rows, setRows] = useState<any[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const pageSize = 20
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [form] = Form.useForm()

  // 筛选
  const [service, setService] = useState('')
  const [changeType, setChangeType] = useState('')

  const load = () => {
    setLoading(true)
    getChanges({ page, page_size: pageSize, service, change_type: changeType })
      .then((r) => {
        const d = r.data
        const list = Array.isArray(d) ? d : d?.changes ?? d?.items ?? d?.data ?? []
        setRows(Array.isArray(list) ? list : [])
        setTotal(Array.isArray(d) ? list.length : Number(d?.total ?? list.length))
      })
      .catch(() => { setRows([]); setTotal(0) })
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [currentClusterId, page, service, changeType])

  const submit = async () => {
    let v: Record<string, string>
    try {
      v = await form.validateFields()
    } catch {
      message.error('请完善必填项后再提交')
      return
    }
    setSubmitting(true)
    try {
      await postChange({
        cluster_id: v.cluster_id,
        service: v.service,
        change_type: v.change_type,
        operator: v.operator,
        content: v.content,
      })
      message.success('变更已登记')
      setModalOpen(false)
      form.resetFields()
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.response?.data?.detail || e?.message || '登记失败')
    } finally {
      setSubmitting(false)
    }
  }

  const cols = [
    {
      title: '时间', dataIndex: 'created_at', key: 'created_at', width: 170,
      render: (v: string, r: any) => <span style={{ fontSize: 12 }}>{fmtTime(v ?? r?.time)}</span>,
    },
    {
      title: '集群', dataIndex: 'cluster_id', key: 'cluster_id', width: 130,
      render: (v: string) => {
        const c = clusters.find((x) => String(x.id) === String(v))
        return <span style={{ fontSize: 12 }}>{c?.name ?? v ?? '-'}</span>
      },
    },
    { title: '服务', dataIndex: 'service', key: 'service', width: 180, render: (v: string) => <span style={{ fontSize: 12, fontWeight: 500 }}>{v || '-'}</span> },
    { title: '类型', dataIndex: 'change_type', key: 'change_type', width: 110, render: changeTag },
    { title: '操作人', dataIndex: 'operator', key: 'operator', width: 110, render: (v: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
    { title: '内容', dataIndex: 'content', key: 'content', ellipsis: true, render: (v: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
  ]

  const clusterOptions = [
    ...clusters.map((c) => ({ value: String(c.id), label: c.name })),
    { value: 'default', label: 'default' },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '基础设施' }, { t: '变更时间线' }]} />
      <PageHeader title="变更时间线" desc="集群/服务变更记录 · 支持按服务与变更类型筛选 · 可手动登记变更"
        actions={
          <Button type="primary" onClick={() => { form.resetFields(); setModalOpen(true) }}>登记变更</Button>
        } />

      <div className="card" style={{ padding: 0 }}>
        <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)', display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <Input
            placeholder="按服务筛选"
            value={service}
            onChange={(e) => { setService(e.target.value); setPage(1) }}
            allowClear
            style={{ width: 220 }}
          />
          <Select
            placeholder="变更类型"
            value={changeType || undefined}
            onChange={(v) => { setChangeType(v || ''); setPage(1) }}
            allowClear
            style={{ width: 160 }}
            options={CHANGE_TYPES.filter((c, i, arr) => arr.findIndex((x) => x.value === c.value) === i)
              .map((c) => ({ value: c.value, label: c.label }))}
          />
          <Button onClick={load} loading={loading}>刷新</Button>
          <span style={{ fontSize: 12, color: 'var(--text-muted)', alignSelf: 'center' }}>共 {total} 条</span>
        </div>
        <Table rowKey={(r: any) => `${r?.id ?? ''}-${r?.created_at ?? ''}-${r?.service ?? ''}-${r?.content ?? ''}`}
          loading={loading} columns={cols} dataSource={rows}
          size="middle" pagination={{ current: page, pageSize, total, showSizeChanger: false,
            onChange: (nextPage) => setPage(nextPage) }} scroll={{ x: 980 }}
          locale={{ emptyText: <AntdEmpty description="暂无变更记录，点击右上角「登记变更」录入" /> }} />
      </div>

      <Modal title="登记变更" open={modalOpen} onOk={submit} confirmLoading={submitting}
        onCancel={() => setModalOpen(false)} destroyOnClose width={560}>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="cluster_id" label="集群" rules={[{ required: true, message: '请选择集群' }]}>
            <Select placeholder="选择集群" options={clusterOptions} showSearch optionFilterProp="label" />
          </Form.Item>
          <Form.Item name="service" label="服务" rules={[{ required: true, message: '请输入服务名' }]}>
            <Input placeholder="如：order-service" />
          </Form.Item>
          <Form.Item name="change_type" label="变更类型" rules={[{ required: true, message: '请选择变更类型' }]}>
            <Select placeholder="选择变更类型"
              options={CHANGE_TYPES.filter((c, i, arr) => arr.findIndex((x) => x.value === c.value) === i)
                .map((c) => ({ value: c.value, label: c.label }))} />
          </Form.Item>
          <Form.Item name="operator" label="操作人" rules={[{ required: true, message: '请输入操作人' }]}>
            <Input placeholder="如：ops-team" />
          </Form.Item>
          <Form.Item name="content" label="变更内容" rules={[{ required: true, message: '请输入变更内容' }]}>
            <Input.TextArea rows={3} placeholder="如：升级镜像 v1.2.0 → v1.3.0，滚动发布" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Changes
