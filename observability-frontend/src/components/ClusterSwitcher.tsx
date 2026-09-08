import React from 'react'
import { Alert, Select, Spin } from 'antd'
import { useScopeStore } from '../store/scopeStore'

// 全局集群选择器：多集群纳管入口。
// 遵循亮色极简设计：复用 token 变量与 antd 标准组件，不引入新风格。
export default function ClusterSwitcher() {
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const clusters = useScopeStore((s) => s.clusters)
  const switchCluster = useScopeStore((s) => s.switchCluster)
  const loading = useScopeStore((s) => s.loading)
  const switching = useScopeStore((s) => s.switching)
  const error = useScopeStore((s) => s.error)

  // cluster_id 语义：始终使用后端返回的 canonical UUID。旧 numeric id/name
  // 不再被当成查询授权上下文，避免 UI 选择器把不可授权的 legacy ref 传给后端。
  // 修复 5.9：option label 增加集群状态图标（● 健康 / ● 降级 / ● 失联 / ● 未知），便于运维一眼识别集群可用性
  const statusDot = (s: string) => {
    const st = String(s || '').toLowerCase()
    if (['healthy', 'active', 'ready', 'running', 'ok'].includes(st)) return { color: '#16a34a', label: '健康' }
    if (['degraded', 'warning'].includes(st)) return { color: '#d97706', label: '降级' }
    if (['down', 'error', 'offline', 'disconnected'].includes(st)) return { color: '#dc2626', label: '失联' }
    return { color: '#a3aebe', label: '未知' }
  }
  const options = clusters.map((c) => {
      const value = c.cluster_id
      const d = statusDot(c.status)
      return {
        value,
        label: c.node_count
          ? `${c.name} (${c.node_count}节点)`
          : c.name,
        labelNode: <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: d.color, display: 'inline-block', flexShrink: 0 }} />
          {c.name}
          {c.node_count ? ` (${c.node_count}节点)` : ''}
        </span>,
      }
    })

  if (error && clusters.length === 0) {
    return <Alert banner type="error" message={error} style={{ maxWidth: 360 }} />
  }

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      {(loading || switching) && <Spin size="small" aria-label="作用域加载中" />}
      <Select
        value={activeClusterId || undefined}
        placeholder="选择作用域"
        onChange={(value) => { void switchCluster(value) }}
        options={options.map((o) => ({ ...o, label: o.labelNode || o.label }))}
        style={{ minWidth: 130 }}
        size="small"
        popupMatchSelectWidth={false}
        title="切换监控集群范围"
        optionLabelProp="label"
      />
    </div>
  )
}
