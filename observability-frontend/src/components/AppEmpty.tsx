import { Empty, Typography } from 'antd'

interface AppEmptyProps {
  description?: string
  height?: number
  tip?: string
}

/** AppEmpty 统一空态组件：跨页面一致的"暂无数据"展示。 */
export default function AppEmpty({ description = '暂无数据', height = 240, tip }: AppEmptyProps) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', minHeight: height }}>
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <div>
            <Typography.Text type="secondary">{description}</Typography.Text>
            {tip && (
              <div>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>{tip}</Typography.Text>
              </div>
            )}
          </div>
        }
      />
    </div>
  )
}
