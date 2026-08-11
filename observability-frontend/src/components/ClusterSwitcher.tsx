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

  // cluster_id 语义：查询层的集群标记。主集群（id===1，数据写入 cluster_id='default'）映射为 'default'；
  // 纳管集群用集群 name 作为 cluster_id 标记（与采集 chart 上报的 cluster_id 对齐）。
  const options = [
    { value: 'all', label: '全部集群' },
    ...clusters.map((c) => ({
      value: c.id === 1 ? 'default' : c.name,
      label: `${c.name}${c.node_count ? ` (${c.node_count}节点)` : ''}`,
    })),
  ]

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <Select
        value={currentClusterId}
        onChange={(v) => setCurrentCluster(v)}
        options={options}
        style={{ minWidth: 130 }}
        size="small"
        popupMatchSelectWidth={false}
        title="切换监控集群范围"
      />
    </div>
  )
}
