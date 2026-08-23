import React from 'react'
import { Alert, Space, Tag } from 'antd'

// P12.7 SSE UX：permission_denied / no_data / unavailable / timeout 必须不同视觉/文案，
// 不得统一为"暂无数据"。
type ToolStatus = 'success' | 'partial' | 'no_data' | 'failed' | 'timeout' | 'unavailable' | 'permission_denied'

const STATUS_VIEW: Record<ToolStatus, { text: string; type: 'success' | 'info' | 'warning' | 'error' }> = {
  success: { text: '查询成功', type: 'success' },
  partial: { text: '部分返回（结果不完整）', type: 'warning' },
  no_data: { text: '无数据（数据源无匹配记录）', type: 'info' },
  failed: { text: '查询失败', type: 'error' },
  timeout: { text: '查询超时（请稍后重试）', type: 'warning' },
  unavailable: { text: '数据源不可用（服务未就绪）', type: 'error' },
  permission_denied: { text: '无权限（权限被拒绝，非数据缺失）', type: 'error' },
}

const STATUS_TAG_TONE: Record<ToolStatus, string> = {
  success: 'green', partial: 'orange', no_data: 'blue', failed: 'red',
  timeout: 'orange', unavailable: 'red', permission_denied: 'red',
}

// P12.7：绝不把 permission_denied/no_data/unavailable/timeout 统一成"暂无数据"。
export const ToolResultStatus: React.FC<{ status?: ToolStatus | string; detail?: string }> = ({ status, detail }) => {
  const key = (status || 'success') as ToolStatus
  const view = STATUS_VIEW[key] ?? STATUS_VIEW.failed
  return (
    <Alert
      type={view.type}
      showIcon
      message={<Space><Tag color={STATUS_TAG_TONE[key] ?? 'red'}>{key}</Tag><span>{view.text}</span></Space>}
      description={detail}
      style={{ marginBottom: 8 }}
    />
  )
}
