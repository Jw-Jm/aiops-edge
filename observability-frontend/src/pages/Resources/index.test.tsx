import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import Resources from './index'

vi.mock('../observability/ServiceObservability', () => ({ default: () => <div>服务全景</div> }))
vi.mock('../infra/K8sActions', () => ({ default: () => <div>Kubernetes 资源</div> }))
vi.mock('../observability/VirtualMachines', () => ({ default: () => <div>虚拟机资源</div> }))
vi.mock('../infra/Hardware', () => ({ default: () => <div>硬件健康</div> }))
vi.mock('../capacity/Capacity', () => ({ default: () => <div>容量预测</div> }))
vi.mock('../observability/ResourceRelationships', () => ({ default: () => <div>资源关系</div> }))

describe('Resources unified entry', () => {
  it('exposes resource views behind one first-level route', () => {
    render(<MemoryRouter initialEntries={['/resources']}><Resources /></MemoryRouter>)
    expect(screen.getByText('服务')).toBeInTheDocument()
    expect(screen.getByText('服务全景')).toBeInTheDocument()
    expect(screen.getByText('Kubernetes')).toBeInTheDocument()
    expect(screen.getByText('虚拟机')).toBeInTheDocument()
  })
})
