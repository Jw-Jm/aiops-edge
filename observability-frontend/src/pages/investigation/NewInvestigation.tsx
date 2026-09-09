import React, { useEffect, useState } from 'react'
import { Alert, Button, Card, Form, Input, Select, Space, Tag, message } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { createRun } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import ScopeBar from '../../features/scope/ScopeBar'
import { draftFromSearchParams } from '../../features/investigation/draft'
import { formatTimeRange, type TimeRange } from '../../features/scope/types'

function absoluteWindow(range: TimeRange): { start: string; end: string } {
  if (range.mode === 'absolute') return { start: range.start, end: range.end }
  const end = new Date()
  const start = new Date(end.getTime() - range.minutes * 60_000)
  return { start: start.toISOString(), end: end.toISOString() }
}

// P12.4：用户显式触发 AI 调查入口。deep-link exact tenant/canonical cluster/resource/time；
// 仅查看页面/切换资源/收到新告警不得产生 AI Run（触发必须是显式按钮）。
const NewInvestigation: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [form] = Form.useForm()
  const clusters = useScopeStore((s) => s.clusters)
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const activeScope = useScopeStore((s) => s.active ?? { tenantId: '', clusterId: activeClusterId, timeRange: { mode: 'relative' as const, minutes: 60 } })
  const selectedNamespace = activeScope.resource?.domain === 'kubernetes' ? activeScope.resource.namespace : undefined
  const activeCluster = clusters.find((cluster) => cluster.cluster_id === activeClusterId)
  const draft = draftFromSearchParams(searchParams)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    form.setFieldsValue({
      clusterId: activeClusterId,
      namespace: draft.namespace || selectedNamespace || undefined,
      resourceId: draft.resourceId || activeScope.resource?.uid || undefined,
      symptom: draft.symptom || undefined,
      targetType: draft.targetType || activeScope.resource?.type || 'service',
      actionMode: draft.actionMode || 'read_only',
    })
  }, [activeClusterId, activeScope.resource?.type, activeScope.resource?.uid, draft.actionMode, draft.namespace, draft.resourceId, draft.symptom, draft.targetType, form, selectedNamespace])

  const onFinish = (values: { resourceId: string; symptom: string; clusterId: string; namespace?: string; targetType: string; actionMode: 'read_only' | 'propose' }) => {
    const clusterId = activeClusterId || values.clusterId
    if (!clusterId) { message.warning('请先选择已授权集群'); return }
    const window = absoluteWindow(activeScope.timeRange)
    setSubmitting(true)
    // P12：真实触发 POST /api/v1/ai/runs（显式按钮才创建，服务器重新鉴权）
    createRun({
      cluster_id: clusterId,
      resource_id: values.resourceId,
      service: values.resourceId,
      target_type: values.targetType,
      intent: values.symptom,
      message: values.symptom,
      namespace: values.namespace || selectedNamespace || undefined,
      query_window_start: window.start,
      query_window_end: window.end,
      action_mode: values.actionMode || 'read_only',
      principal_type: 'user',
    })
      .then((resp) => {
        message.success('调查已发起')
        navigate(`/investigation/${resp.data.run_id}`)
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
      <div style={{ maxWidth: 760 }}>
        <ScopeBar />
        <Card size="small" title="调查范围" style={{ marginTop: 12 }}>
          <Space wrap>
            <Tag color="blue">生产环境</Tag>
            <Tag>集群：{activeCluster?.name || activeClusterId || '未选择'}</Tag>
            {selectedNamespace && <Tag>命名空间：{selectedNamespace}</Tag>}
            {(draft.resourceId || activeScope.resource?.uid) && <Tag>资源：{draft.resourceId || activeScope.resource?.uid}</Tag>}
            <Tag>时间：{formatTimeRange(activeScope.timeRange)}</Tag>
          </Space>
          {draft.problemId && <div style={{ marginTop: 10, color: 'var(--text-secondary)', fontSize: 12 }}>来源问题：{draft.problemId}</div>}
        </Card>
        {!activeClusterId && <Alert showIcon type="warning" message="尚未选择已授权集群" description="先在顶部作用域选择器选择集群，调查请求才会提交。" style={{ marginTop: 12 }} />}
        <Card size="small" title="调查目标" style={{ marginTop: 12 }}>
          <Form form={form} layout="vertical" onFinish={onFinish}>
          <Form.Item name="clusterId" hidden><Input /></Form.Item>
          <Form.Item name="namespace" hidden><Input /></Form.Item>
          <Form.Item name="targetType" label="目标类型" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'service', label: '服务' },
                { value: 'node', label: 'Kubernetes 节点' },
                { value: 'deployment', label: 'Deployment' },
                { value: 'statefulset', label: 'StatefulSet' },
                { value: 'daemonset', label: 'DaemonSet' },
                { value: 'pod', label: 'Pod' },
                { value: 'namespace', label: 'Namespace' },
                { value: 'vm', label: '虚拟机' },
              ]}
            />
          </Form.Item>
          <Form.Item name="resourceId" label="资源" rules={[{ required: true, message: '请输入资源标识' }]}>
            <Input placeholder="svc/checkout" />
          </Form.Item>
          <Form.Item name="symptom" label="症状 / 调查目标" rules={[{ required: true, message: '请输入症状' }]}>
            <Input.TextArea placeholder="service error rate spike" rows={3} />
          </Form.Item>
          <Form.Item name="actionMode" label="动作权限">
            <Select options={[{ value: 'read_only', label: '只读调查（默认）' }, { value: 'propose', label: '允许提出处置建议（仍需审批）' }]} />
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
    </div>
  )
}

export default NewInvestigation
