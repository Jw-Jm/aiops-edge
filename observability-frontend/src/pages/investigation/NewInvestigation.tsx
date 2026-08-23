import React, { useState } from 'react'
import { Button, Card, Form, Input, Select, Space, message } from 'antd'
import { useNavigate } from 'react-router-dom'
import { TENANT_ID, createRun } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

// P12.4：用户显式触发 AI 调查入口。deep-link exact tenant/canonical cluster/resource/time；
// 仅查看页面/切换资源/收到新告警不得产生 AI Run（触发必须是显式按钮）。
const NewInvestigation: React.FC = () => {
  const navigate = useNavigate()
  const [form] = Form.useForm()
  const clusters = useUIStore((s) => s.clusters)
  const [submitting, setSubmitting] = useState(false)

  const onFinish = (values: { resourceId: string; symptom: string; clusterId: string }) => {
    setSubmitting(true)
    // P12：真实触发 POST /api/v1/ai/runs（显式按钮才创建，服务器重新鉴权）
    createRun({
      tenant_id: TENANT_ID,
      cluster_id: values.clusterId,
      resource_id: values.resourceId,
      intent: values.symptom,
      action_mode: 'read_only',
      principal_type: 'user',
    })
      .then(() => {
        message.success('调查已发起')
        navigate('/investigation')
      })
      .catch(() => {
        message.error('发起失败：后端 Run API 不可用或鉴权被拒')
        setSubmitting(false)
      })
  }

  return (
    <div>
      <PageHeader
        title="发起 AI 调查"
        desc="显式人工触发；不随页面加载/告警到达自动创建"
        actions={<Button onClick={() => window.history.back()}>返回</Button>}
      />
      <Card size="small" style={{ maxWidth: 560 }}>
        <Form form={form} layout="vertical" onFinish={onFinish} initialValues={{ clusterId: clusters[0]?.id ?? '' }}>
          <Form.Item name="clusterId" label="Canonical Cluster" rules={[{ required: true }]}>
            <Select
              placeholder="选择 canonical cluster"
              options={clusters.map((c) => ({ value: c.id, label: `${c.name} (${c.id})` }))}
            />
          </Form.Item>
          <Form.Item name="resourceId" label="资源" rules={[{ required: true, message: '请输入资源标识' }]}>
            <Input placeholder="svc/checkout" />
          </Form.Item>
          <Form.Item name="symptom" label="症状 / 调查目标" rules={[{ required: true, message: '请输入症状' }]}>
            <Input.TextArea placeholder="service error rate spike" rows={3} />
          </Form.Item>
          <Form.Item>
            <Space>
              <Button type="primary" htmlType="submit" loading={submitting}>发起调查</Button>
              <Button onClick={() => navigate('/investigation')}>取消</Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  )
}

export default NewInvestigation
