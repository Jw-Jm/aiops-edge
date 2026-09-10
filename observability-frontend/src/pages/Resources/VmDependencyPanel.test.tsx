import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import VmDependencyPanel from './VmDependencyPanel'

describe('VmDependencyPanel', () => {
  it('makes storage identity and Multus semantics explicit', () => {
    render(<VmDependencyPanel dependencies={{
      disks: [{ deviceName: 'rootdisk', bus: 'virtio', volumeName: 'rootdisk', pvc: { name: 'pvc-root', namespace: 'prod' }, pv: { name: 'pv-root' } }],
      networks: [
        { interfaceName: 'default', binding: 'bridge', defaultPodNetwork: true },
        { interfaceName: 'net1', binding: 'sriov', defaultPodNetwork: false, nad: { name: 'nad-sriov-prod', namespace: 'prod' }, cni: { name: 'sriov-cni' } },
      ],
    }} />)
    expect(screen.getByText('磁盘与卷')).toBeInTheDocument()
    expect(screen.getByText('网络接口')).toBeInTheDocument()
    expect(screen.getByText('默认 Pod 网络（无 NAD）')).toBeInTheDocument()
    expect(screen.getByText('Multus 辅助网络定义')).toBeInTheDocument()
    expect(screen.getByText('prod / pvc-root')).toBeInTheDocument()
    expect(screen.queryByText('虚拟机磁盘资源')).not.toBeInTheDocument()
  })

  it('does not fabricate dependencies when the DTO is empty', () => {
    render(<VmDependencyPanel dependencies={{ disks: [], networks: [] }} />)
    expect(screen.getByText('暂无已确认的磁盘或网络依赖')).toBeInTheDocument()
  })
})
