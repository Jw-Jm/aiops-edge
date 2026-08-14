import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, InputNumber, Switch, Drawer, message } from 'antd'
import { useNavigate } from 'react-router-dom'
import { getAlertRules, createAlertRule, deleteAlertRule } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

interface Rule { id: string; name?: string; rule_name?: string; service_name?: string; metric?: string; threshold?: number; severity?: string; enabled?: boolean; condition?: string; duration?: number; cooldown?: number }

const AlertRules: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const navigate = useNavigate()
  const [data, setData] = useState<Rule[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<Rule | null>(null) // 2.11 规则查看
  const [form] = Form.useForm()

  const load = () => {
    setLoading(true)
    getAlertRules().then((r) => {
      const d = Array.isArray(r.data) ? r.data : r.data?.rules || r.data?.data || []
      setData(d)
    }).catch(() => setData([])).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [currentClusterId])

  const submit = async () => {
    const v = await form.validateFields()
    // P0: 契约对齐后端 AlertRule 结构体字段（name/service 而非 rule_name/service_name）。
    // 后端 AlertRule 结构体 json tag 为 name/service，且 service 必填。
    createAlertRule({
      name: v.name,
      service: v.service,
      metric: v.metric,
      threshold: v.threshold,
      severity: v.severity,
      condition: v.condition || '>',
      duration: v.duration || 5,
      // P0-4 修复：规则类型可选（threshold/anomaly/burn_rate/log/trace_*）
      type: v.type || 'threshold',
      anomaly_method: v.anomaly_method,
      baseline_seconds: v.baseline_seconds,
      keyword: v.keyword,
      slo_id: v.slo_id,
      enabled: v.enabled ?? true,
    })
      .then(() => { message.success('已创建'); setOpen(false); load() })
      .catch((e) => message.error(e?.response?.data?.error || '创建失败'))
  }

  const cols = [
    { title: '规则名', dataIndex: 'name', key: 'name', render: (_: any, r: Rule) => r.name || r.rule_name },
    { title: '服务', dataIndex: 'service_name', key: 'service_name', render: (v: string) => v || '所有服务' },
    { title: '指标', dataIndex: 'metric', key: 'metric' },
    { title: '阈值', dataIndex: 'threshold', key: 'threshold', render: (v: number) => v ?? '-' },
    { title: '严重度', dataIndex: 'severity', key: 'severity', render: (v: string) => <StatusBadge text={v || 'warning'} tone={v === 'critical' ? 'crit' : v === 'warning' ? 'warn' : 'info'} /> },
    { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 80, render: (v: boolean) => <StatusBadge text={v ? '启用' : '停用'} tone={v ? 'ok' : 'muted'} /> },
    { title: '操作', key: 'op', width: 200, render: (_: any, r: Rule) => (
        <span style={{ display: 'inline-flex', gap: 4 }}>
          <Button size="small" type="link" onClick={() => setView(r)}>详情</Button>
          <Button size="small" type="link" onClick={() => navigate(`/alerts/events?rule=${encodeURIComponent(r.id || r.name || '')}`)}>历史告警</Button>
          <Button size="small" type="link" danger onClick={() => deleteAlertRule(String(r.id)).then(() => { message.success('已删除'); load() }).catch(() => message.error('删除失败'))}>删除</Button>
        </span>
      ) },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '告警' }, { t: '告警规则' }]} />
      <PageHeader title="告警规则" desc="管理阈值、异常检测、燃烧速率等告警策略"
        actions={<Button type="primary" onClick={() => setOpen(true)}>新建规则</Button>} />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={false} locale={{ emptyText: <Empty text="暂无告警规则" /> }} />
      </div>

      <Modal title="新建告警规则" open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="name" label="规则名称" rules={[{ required: true }]}><Input placeholder="如：order-svc 错误率过高" /></Form.Item>
          <Form.Item name="service" label="服务名称" rules={[{ required: true, message: '服务名称必填' }]}><Input placeholder="如：order-svc" /></Form.Item>
          <Form.Item name="condition" label="触发条件" initialValue=">"><Select options={[{ value: '>', label: '>' }, { value: '>=', label: '>=' }, { value: '<', label: '<' }, { value: '<=', label: '<=' }]} /></Form.Item>
          <Form.Item name="duration" label="持续时间(分钟)" initialValue={5}><InputNumber style={{ width: '100%' }} min={1} /></Form.Item>
          <Form.Item name="type" label="规则类型" initialValue="threshold">
            <Select options={[
              { value: 'threshold', label: '阈值告警' },
              { value: 'trace_error_rate', label: '链路错误率' },
              { value: 'trace_latency', label: '链路延迟' },
              { value: 'anomaly', label: '异常检测(zscore)' },
              { value: 'burn_rate', label: 'SLO 烧毁率' },
              { value: 'log', label: '日志关键字' },
            ]} />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(prev, cur) => prev.type !== cur.type}>
            {({ getFieldValue }) => {
              const t = getFieldValue('type') || 'threshold'
              return (
                <>
                  {t === 'anomaly' && (
                    <>
                      <Form.Item name="anomaly_method" label="检测方法" initialValue="zscore">
                        <Select options={[{ value: 'zscore', label: 'Z-Score' }, { value: 'mad', label: 'MAD' }]} />
                      </Form.Item>
                      <Form.Item name="baseline_seconds" label="基线窗口(秒)" initialValue={900}><InputNumber style={{ width: '100%' }} min={60} /></Form.Item>
                    </>
                  )}
                  {t === 'burn_rate' && (
                    <Form.Item name="slo_id" label="关联 SLO ID"><Input placeholder="SLO 目标 id（可在 SLO 页面查看）" /></Form.Item>
                  )}
                  {t === 'log' && (
                    <Form.Item name="keyword" label="日志关键字"><Input placeholder="如：connection refused" /></Form.Item>
                  )}
                  {t !== 'anomaly' && (
                    <>
                      <Form.Item name="metric" label="监控指标" rules={[{ required: true }]}><Input placeholder="如：error_rate" /></Form.Item>
                      <Form.Item name="threshold" label="阈值" rules={[{ required: true }]}><InputNumber style={{ width: '100%' }} /></Form.Item>
                    </>
                  )}
                </>
              )
            }}
          </Form.Item>
          <Form.Item name="severity" label="严重度" initialValue="warning"><Select options={[{ value: 'critical', label: '严重' }, { value: 'warning', label: '警告' }, { value: 'info', label: '信息' }]} /></Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked" initialValue={true}><Switch /></Form.Item>
        </Form>
      </Modal>

      {/* 2.11 规则查看：完整配置 */}
      <Drawer width={440} open={!!view} onClose={() => setView(null)} title="规则详情" styles={{ body: { padding: 16 } }}>
        {view && (
          <Table rowKey="k" size="small" pagination={false} showHeader={false}
            dataSource={[
              { k: '规则名', v: view.name || view.rule_name || '-' },
              { k: '服务', v: view.service_name || '-' },
              { k: '指标', v: view.metric || '-' },
              { k: '阈值', v: view.threshold != null ? `${view.threshold}` : '-' },
              { k: '条件', v: view.condition || 'threshold' },
              { k: '严重度', v: view.severity || 'warning' },
              { k: '持续时间', v: view.duration != null ? `${view.duration} 分钟` : '-' },
              { k: '冷却期', v: view.cooldown != null ? `${view.cooldown} 秒` : '-' },
              { k: '状态', v: view.enabled ? '启用' : '停用' },
            ]}
            columns={[
              { title: '', dataIndex: 'k', key: 'k', width: 100, render: (v: string) => <span style={{ color: 'var(--text-muted)', fontWeight: 600 }}>{v}</span> },
              { title: '', dataIndex: 'v', key: 'v', render: (v: string) => <span>{v}</span> },
            ]}
          />
        )}
      </Drawer>
    </div>
  )
}

export default AlertRules
