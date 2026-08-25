import React, { useEffect } from 'react'
import { Select } from 'antd'
import { useUIStore } from '../store/uiStore'

// 全局集群选择器：多集群纳管入口。
// 遵循亮色极简设计：复用 token 变量与 antd 标准组件，不引入新风格。
export default function ClusterSwitcher() {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)
  const setCurrentCluster = useUIStore((s) => s.setCurrentCluster)
  const refreshClusters = useUIStore((s) => s.refreshClusters)

  // 首次挂载拉取集群列表（幂等，不影响页面数据加载）
  useEffect(() => {
    if (clusters.length === 0) {
      refreshClusters()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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
  const options = [
    { value: 'all', label: '全部集群', labelNode: <span>全部集群</span> },
    ...clusters.map((c) => {
      const d = statusDot(c.status)
      return {
        value: c.cluster_id || `legacy-${c.id}`,
        label: c.node_count
          ? `${c.name} (${c.node_count}节点)`
          : c.name,
        // 用 ReactNode 渲染状态点，Select 支持
        labelNode: <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: d.color, display: 'inline-block', flexShrink: 0 }} />
          {c.name}
          {c.node_count ? ` (${c.node_count}节点)` : ''}
        </span>,
      }
    }),
  ]

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <Select
        value={currentClusterId}
        onChange={(v) => setCurrentCluster(v)}
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
