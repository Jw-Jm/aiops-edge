import React from 'react'
import { Card, Descriptions, Empty, Tag, Typography } from 'antd'
import type { ResourceRef, VmDependencies, VmDiskDependency, VmNetworkDependency } from './resourceDetailModel'

function refLabel(value?: ResourceRef): string {
  if (!value) return '未发现'
  return value.namespace ? `${value.namespace} / ${value.name}` : value.name
}

function diskItems(disk: VmDiskDependency) {
  return [
    { key: 'device', label: '设备', children: disk.deviceName },
    { key: 'bus', label: '总线', children: disk.bus || '未提供' },
    { key: 'volume', label: '卷引用', children: disk.volumeName },
    { key: 'data-volume', label: 'DataVolume', children: refLabel(disk.dataVolume) },
    { key: 'pvc', label: 'PVC', children: refLabel(disk.pvc) },
    { key: 'pv', label: 'PV', children: refLabel(disk.pv) },
    { key: 'storage-class', label: 'StorageClass', children: refLabel(disk.storageClass) },
  ]
}

function networkItems(network: VmNetworkDependency) {
  return [
    { key: 'interface', label: '接口', children: network.interfaceName },
    { key: 'binding', label: '绑定方式', children: network.binding },
    { key: 'network', label: '网络', children: network.defaultPodNetwork ? '默认 Pod 网络（无 NAD）' : refLabel(network.network) },
    { key: 'nad', label: 'NAD', children: network.defaultPodNetwork ? <Tag>默认网络</Tag> : <span>{refLabel(network.nad)} <Typography.Text type="secondary">Multus 辅助网络定义</Typography.Text></span> },
    { key: 'cni', label: 'CNI', children: refLabel(network.cni) },
  ]
}

export const VmDependencyPanel: React.FC<{ dependencies?: VmDependencies }> = ({ dependencies }) => {
  if (!dependencies || (!dependencies.disks.length && !dependencies.networks.length)) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无已确认的磁盘或网络依赖" />
  return <section className="vm-dependency-panel" aria-label="虚拟机依赖关系">
    <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>虚拟机依赖关系</Typography.Title><Typography.Text type="secondary">磁盘是设备视图，PVC/PV 保持唯一 Kubernetes 资源身份；默认 Pod 网络不创建 NAD。</Typography.Text></div></div>
    <div className="vm-dependency-panel__grid">
      <Card size="small" title="磁盘与卷">
        {dependencies.disks.length ? dependencies.disks.map((disk) => <Card.Grid key={`${disk.deviceName}:${disk.volumeName}`} hoverable={false} style={{ width: '100%' }}><Descriptions size="small" column={1} items={diskItems(disk)} /></Card.Grid>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未发现磁盘依赖" />}
      </Card>
      <Card size="small" title="网络接口">
        {dependencies.networks.length ? dependencies.networks.map((network) => <Card.Grid key={network.interfaceName} hoverable={false} style={{ width: '100%' }}><Descriptions size="small" column={1} items={networkItems(network)} /></Card.Grid>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未发现网络接口依赖" />}
      </Card>
    </div>
  </section>
}

export default VmDependencyPanel
