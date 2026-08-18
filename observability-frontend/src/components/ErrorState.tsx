import React from 'react'
import { Alert, Button } from 'antd'

/**
 * 统一错误态组件：展示加载失败信息 + 重试按钮（配合 useAsyncData 使用，见 F2）。
 * 用于区分"加载失败"与"无数据"两种空态。
 */
export const ErrorState: React.FC<{ message?: string; onRetry?: () => void }> = ({ message, onRetry }) => (
  <Alert
    type="error"
    showIcon
    message="加载失败"
    description={message || '请稍后重试'}
    action={
      onRetry ? (
        <Button size="small" danger onClick={onRetry}>
          重试
        </Button>
      ) : undefined
    }
    style={{ margin: 16 }}
  />
)

export default ErrorState