import React, { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Form, Input, Radio, Space, Tag, message } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { createRun } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import ScopeBar from '../../features/scope/ScopeBar'
import ResourcePicker from '../../features/scope/ResourcePicker'
import { draftFromSearchParams } from '../../features/investigation/draft'
import { formatTimeRange, type ActiveScope } from '../../features/scope/types'
import { toLegacyRunScope } from '../../features/scope/runScopeAdapter'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import type { PlatformResourceRef } from '../../features/resources/types'

type InvestigationFormValues = {
  clusterId: string
  symptom: string
  actionMode: 'read_only' | 'propose'
  scopeKind: 'resource' | 'cluster'
  resourceId?: string
  targetType?: string
}

// P12.4：用户显式触发 AI 调查入口。页面加载、Scope 切换、告警到达均不得创建 Run；
// 只有提交按钮会把当前 typed resource 与时间范围冻结为一次 Run 快照。
const NewInvestigation: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [form] = Form.useForm<InvestigationFormValues>()
  const clusters = useScopeStore((s) => s.clusters)
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const activeScope = useScopeStore((s) => s.active ?? { tenantId: '', clusterId: activeClusterId, timeRange: { mode: 'relative' as const, minutes: 60 } })
  const draft = useMemo(() => draftFromSearchParams(searchParams), [searchParams])
  const [draftResource, setDraftResource] = useState<PlatformResourceRef | undefined>(draft.resource)
  const [scopeKind, setScopeKind] = useState<'resource' | 'cluster'>(draft.resource ? 'resource' : 'cluster')
  const [submitting, setSubmitting] = useState(false)

  const cluster = clusters.find((item) => item.cluster_id === activeClusterId)
  const selectedResource = scopeKind === 'resource'
    ? (activeScope.resource?.clusterId === activeClusterId ? activeScope.resource : draftResource?.clusterId === activeClusterId ? draftResource : undefined)
    : undefined
  const selectedNamespace = selectedResource?.domain === 'kubernetes' ? selectedResource.namespace : undefined
  const selectedClusterId = activeClusterId || activeScope.clusterId || draft.clusterId

  useEffect(() => {
    form.setFieldsValue({
      clusterId: selectedClusterId,
      resourceId: selectedResource?.uid,
      targetType: selectedResource?.type,
      symptom: draft.symptom || undefined,
      actionMode: draft.actionMode || 'read_only',
      scopeKind,
    })
  }, [draft.actionMode, draft.symptom, form, scopeKind, selectedClusterId, selectedResource?.type, selectedResource?.uid])

  const onResourceChange = (resource?: PlatformResourceRef) => {
    setDraftResource(resource)
    setScopeKind(resource ? 'resource' : 'cluster')
    form.setFieldsValue({ resourceId: resource?.uid, targetType: resource?.type, scopeKind: resource ? 'resource' : 'cluster' })
  }

  const onFinish = (values: InvestigationFormValues) => {
    const clusterId = activeClusterId || values.clusterId
    if (!clusterId) { message.warning('请先选择已授权集群'); return }
    const resource = values.scopeKind === 'resource' ? selectedResource : undefined
    if (values.scopeKind === 'resource' && !resource) { message.warning('请选择属于当前集群的平台资源'); return }
    const runScope: ActiveScope = { ...activeScope, clusterId, resource }
    const legacy = toLegacyRunScope(runScope, new Date())
    const targetType = resource?.type || 'cluster'
    setSubmitting(true)
    // 显式触发 POST /api/v1/ai/runs；同时保留旧字段兼容层和 canonical absolute window。
    createRun({
      environment: legacy.environment,
      cluster_id: legacy.cluster_id,
      ...(legacy.namespace ? { namespace: legacy.namespace } : {}),
      ...(legacy.target_resource_id ? { resource_id: legacy.target_resource_id, service: legacy.target_resource_id } : {}),
      target_type: targetType,
      intent: values.symptom,
      message: values.symptom,
      action_mode: values.actionMode || 'read_only',
      time_start: legacy.time_start,
      time_end: legacy.time_end,
      time_range_start: legacy.time_start,
      time_range_end: legacy.time_end,
      query_window_start: legacy.time_start,
      query_window_end: legacy.time_end,
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
        desc="显式人工触发；不随页面加载、Scope 切换或告警到达自动创建"
        actions={<Button onClick={() => window.history.back()}>返回</Button>}
      />
      <div style={{ maxWidth: 860 }}>
        <ScopeBar />
        <Card size="small" title="调查范围" style={{ marginTop: 12 }}>
          <Space wrap>
            <Tag color="blue">生产平台</Tag>
            <Tag>集群：{cluster?.name || selectedClusterId || '未选择'}</Tag>
            {selectedResource
              ? <Tag color="geekblue">{resourceTypeLabel(selectedResource.type)} · {resourceLocation(selectedResource)}</Tag>
              : <Tag>集群范围</Tag>}
            {selectedNamespace && <Tag>命名空间：{selectedNamespace}</Tag>}
            <Tag>时间：{formatTimeRange(activeScope.timeRange)}</Tag>
          </Space>
          {draft.problemId && <div style={{ marginTop: 10, color: 'var(--text-secondary)', fontSize: 12 }}>来源问题：{draft.problemId}</div>}
        </Card>
        {!activeClusterId && <Alert showIcon type="warning" message="尚未选择已授权集群" description="先在顶部作用域选择器选择集群，调查请求才会提交。" style={{ marginTop: 12 }} />}
        <Card size="small" title="调查目标" style={{ marginTop: 12 }}>
          <Form form={form} layout="vertical" onFinish={onFinish}>
            <Form.Item name="clusterId" hidden><Input /></Form.Item>
            <Form.Item name="resourceId" hidden><Input /></Form.Item>
            <Form.Item name="targetType" hidden><Input /></Form.Item>
            <Form.Item name="scopeKind" label="调查范围" rules={[{ required: true }]}>
              <Radio.Group onChange={(event) => {
                const next = event.target.value as 'resource' | 'cluster'
                setScopeKind(next)
                if (next === 'cluster') form.setFieldsValue({ resourceId: undefined, targetType: undefined })
              }}>
                <Radio value="resource">指定平台资源</Radio>
                <Radio value="cluster">集群范围</Radio>
              </Radio.Group>
            </Form.Item>
            {scopeKind === 'resource' && (
              <Form.Item label="平台资源" required>
                <ResourcePicker clusterId={selectedClusterId} value={selectedResource} onChange={onResourceChange} disabled={!selectedClusterId} />
                <div style={{ marginTop: 6, color: 'var(--text-muted)', fontSize: 12 }}>资源类型由目录返回结果确定，不允许手工拼接 UID。</div>
              </Form.Item>
            )}
            <Form.Item label="目标类型">
              <Tag>{selectedResource ? resourceTypeLabel(selectedResource.type) : '集群范围'}</Tag>
            </Form.Item>
            <Form.Item name="symptom" label="症状 / 调查目标" rules={[{ required: true, message: '请输入症状' }]}>
              <Input.TextArea placeholder="例如：错误率突增且影响支付请求" rows={3} />
            </Form.Item>
            <Form.Item name="actionMode" label="动作权限">
              <Radio.Group options={[{ value: 'read_only', label: '只读调查（默认）' }, { value: 'propose', label: '允许提出处置建议（仍需审批）' }]} />
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
